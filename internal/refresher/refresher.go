// Package refresher installs the OS scheduler entry that runs `refresh --all --background`.
package refresher

import (
	"bytes"
	"context"
	"embed"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"
	"unicode"

	"github.com/123yogin/oci-assume/internal/procattr"
	"github.com/123yogin/oci-assume/internal/state"
)

// Names of the scheduler entries on each OS.
const (
	Label       = "io.github.123yogin.oci-assume"
	TaskName    = "oci-assume-refresh"
	serviceName = "oci-assume.service"
	timerName   = "oci-assume.timer"
	cronMarker  = "# oci-assume"
)

// ErrUnsupported means this OS has no scheduler backend.
var ErrUnsupported = errors.New("daemon is not supported on this OS")

// ErrLastRun marks a Status error from reading the last-result state, not from the scheduler.
var ErrLastRun = errors.New("cannot read last refresh result")

// Status describes the installed entry and the most recent background run.
type Status struct {
	Installed bool
	Path      string
	LastRun   time.Time
	LastExit  int
}

// Backend installs, removes and inspects the scheduler entry.
type Backend interface {
	Install(bin string) error
	Uninstall() error
	Status() (Status, error)
}

//go:embed templates/*.tmpl
var tmplFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{"xml": xmlEscape, "unit": unitQuote, "unitEnv": unitEnvQuote}).
	ParseFS(tmplFS, "templates/*.tmpl"))

// execFunc runs name with args, feeding stdin when non-empty; it returns stdout, with stderr in the error.
type execFunc func(stdin, name string, args ...string) (string, error)

// sys is everything a backend touches outside the process.
type sys struct {
	exec      execFunc
	home      string
	configDir string
	tmpDir    string
	uid       int
	getenv    func(string) string
	results   func() (map[string]state.Result, error)
}

// ForOS returns the backend for goos wired to the real system.
func ForOS(goos string) (Backend, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return nil, fmt.Errorf("%w (%s)", ErrUnsupported, goos)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	s := &sys{exec: realExec, home: home, configDir: cfg, tmpDir: os.TempDir(), uid: os.Getuid(),
		getenv: os.Getenv, results: loadResults}
	switch goos {
	case "darwin":
		return &launchd{s}, nil
	case "linux":
		return &systemd{s}, nil
	}
	return &schtasks{s: s}, nil
}

// Render returns the scheduler files for goos (filename → content), using $PATH and the home dir.
func Render(goos, bin string) (map[string]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return renderUser(goos, bin, os.Getenv("PATH"), home, windowsUser(os.Getenv), captureEnv(os.Getenv))
}

// captured are the variables a scheduled run inherits from install time, when set.
var captured = []string{"OCI_ASSUME_OCI_BIN", "OCI_ASSUME_STATE_DIR", "OCI_CLI_CONFIG_FILE"}

type envVar struct{ Key, Val string }

func captureEnv(getenv func(string) string) []envVar {
	var out []envVar
	for _, k := range captured {
		if v := getenv(k); v != "" {
			out = append(out, envVar{k, v})
		}
	}
	return out
}

type tmplData struct {
	Label, Bin, Path, Log, Args string
	User                        string // Windows logon trigger account
	Env                         []envVar
}

// validate refuses values that would break or inject into a scheduler entry.
func validate(goos, bin, path string, env []envVar) error {
	vals := []struct{ name, val string }{{"binary path", bin}, {"PATH", path}}
	for _, e := range env {
		vals = append(vals, struct{ name, val string }{e.Key, e.Val})
	}
	for _, v := range vals {
		if strings.ContainsFunc(v.val, unicode.IsControl) {
			return fmt.Errorf("%s %q contains control characters; cannot write a scheduler entry", v.name, v.val)
		}
	}
	if goos == "windows" {
		for _, e := range env {
			if strings.Contains(e.Val, `"`) {
				return fmt.Errorf("%s %q contains a double quote; cannot write a scheduler entry", e.Key, e.Val)
			}
		}
	}
	return nil
}

func render(goos, bin, path, home string, env []envVar) (map[string]string, error) {
	return renderUser(goos, bin, path, home, "", env)
}

func renderUser(goos, bin, path, home, user string, env []envVar) (map[string]string, error) {
	if err := validate(goos, bin, path, env); err != nil {
		return nil, err
	}
	if strings.ContainsFunc(user, unicode.IsControl) {
		return nil, fmt.Errorf("user %q contains control characters; cannot write a scheduler entry", user)
	}
	d := tmplData{Label: Label, Bin: bin, Path: path, Log: logPath(home), Args: winArgs(env), User: user}
	for _, e := range env {
		if e.Val != "" {
			d.Env = append(d.Env, e)
		}
	}
	var names map[string]string
	switch goos {
	case "darwin":
		names = map[string]string{Label + ".plist": "launchd.plist.tmpl"}
	case "linux":
		names = map[string]string{serviceName: "oci-assume.service.tmpl", timerName: "oci-assume.timer.tmpl"}
	case "windows":
		names = map[string]string{"task.xml": "task.xml.tmpl"}
	default:
		return nil, fmt.Errorf("%w (%s)", ErrUnsupported, goos)
	}
	out := map[string]string{}
	for file, t := range names {
		var b bytes.Buffer
		if err := tmpl.ExecuteTemplate(&b, t, d); err != nil {
			return nil, err
		}
		out[file] = b.String()
	}
	if goos == "linux" {
		out["crontab"] = cronLine(bin, path, d.Env) + "\n"
	}
	return out, nil
}

// winArgs passes captured variables as hidden --env flags; Task Scheduler has no environment block.
func winArgs(env []envVar) string {
	s := "refresh --all --background"
	for _, e := range env {
		if e.Val == "" {
			continue
		}
		v := e.Key + "=" + e.Val
		// Backslashes before the closing quote are doubled (CommandLineToArgvW rules).
		trail := len(v) - len(strings.TrimRight(v, `\`))
		s += ` --env "` + v + strings.Repeat(`\`, trail) + `"`
	}
	return s
}

// logPath is darwin-only, so it is joined with "/" to keep goldens identical on every host.
func logPath(home string) string {
	return strings.TrimRight(home, "/") + "/Library/Logs/oci-assume.log"
}

// BinPath is the running executable, preferring a PATH entry or Homebrew's stable link over a versioned path.
func BinPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return binPathFrom(p, exec.LookPath)
}

func binPathFrom(exe string, lookPath func(string) (string, error)) (string, error) {
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if lp, err := lookPath("oci-assume"); err == nil {
		if abs, err := filepath.Abs(lp); err == nil && sameFile(abs, resolved) {
			return abs, nil
		}
	}
	return stableBin(resolved), nil
}

// stableBin swaps a versioned Cellar or Caskroom path for <prefix>/bin/oci-assume when that resolves to the same file.
func stableBin(resolved string) string {
	for _, dir := range []string{"/Cellar/oci-assume/", "/Caskroom/oci-assume/"} {
		if i := strings.Index(resolved, dir); i >= 0 {
			if cand := resolved[:i] + "/bin/oci-assume"; sameFile(cand, resolved) {
				return cand
			}
		}
	}
	return resolved
}

func sameFile(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	return err == nil && ra == rb
}

// lastRun returns the newest background result time; exit is 0 only when every one is OK.
func (s *sys) lastRun(st *Status) error {
	rs, err := s.results()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLastRun, err)
	}
	for _, r := range rs {
		if !r.Background {
			continue
		}
		if r.At.After(st.LastRun) {
			st.LastRun = r.At
		}
		if !r.OK {
			st.LastExit = 1
		}
	}
	return nil
}

func loadResults() (map[string]state.Result, error) {
	s, err := state.Load()
	return s.LastResult, err
}

func realExec(stdin, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: runs the platform scheduler tool by design
	procattr.Hide(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// unitQuote double-quotes s for ExecStart, escaping quotes, backslashes, % and $.
func unitQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

// unitEnvQuote quotes for Environment=, which expands % specifiers but not $.
func unitEnvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s) + `"`
}

// writeFile writes content to path, creating parent directories.
func writeFile(path, content string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), perm)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
