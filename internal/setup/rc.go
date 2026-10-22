package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/shell"
)

const (
	markBegin = "# >>> oci-assume >>>"
	markEnd   = "# <<< oci-assume <<<"
	bakSuffix = ".oci-assume.bak"
	execWait  = 10 * time.Second
)

// ErrUnknownShell means no flag, parent process or $SHELL named a supported shell.
var ErrUnknownShell = errors.New("could not detect your shell")

// windowsParent names the parent process on Windows; a var so tests can fake it.
var windowsParent = parentProcessName

// Target is one file the hook step writes: Kind "block" (marked region) or "dropin" (whole file).
type Target struct {
	Path, Kind, Content string
}

// hookLine is the guarded line for each shell; static apart from the shell name.
func hookLine(sh string) string {
	switch sh {
	case "zsh", "bash":
		return `command -v oci-assume >/dev/null && eval "$(oci-assume init ` + sh + `)"`
	case "fish":
		return "if command -q oci-assume; oci-assume init fish | source; end"
	case "pwsh":
		return "if (Get-Command oci-assume -ErrorAction SilentlyContinue) { oci-assume init pwsh | Out-String | Invoke-Expression }"
	}
	return ""
}

const fishHeader = "# Added by oci-assume setup; removed by oci-assume uninstall.\n"

// bashProfileLine makes macOS login shells (Terminal.app) read ~/.bashrc.
const bashProfileLine = `[ -f ~/.bashrc ] && . ~/.bashrc`

var sourcesBashrc = regexp.MustCompile(`(^|[\s;&|])(source|\.)\s+\S*\.bashrc`)

// DetectShell resolves the shell: flag, parent process, $SHELL, then pwsh on windows.
func DetectShell(e *Env, flag string) (string, error) {
	if flag != "" {
		s, err := shell.Parse(flag)
		return string(s), err
	}
	if s := normShell(parentName(e)); s != "" {
		return s, nil
	}
	if s := normShell(getenv(e, "SHELL")); s != "" {
		return s, nil
	}
	if e.GOOS == "windows" {
		return "pwsh", nil
	}
	return "", ErrUnknownShell
}

func parentName(e *Env) string {
	if e.GOOS == "windows" {
		n, _ := windowsParent()
		return n
	}
	out, _ := run(e, "ps", "-o", "comm=", "-p", strconv.Itoa(os.Getppid()))
	return out
}

// normShell maps "-zsh", "/bin/bash" or "powershell.exe" to a supported name, else "".
func normShell(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.ToLower(strings.TrimPrefix(s, "-")), ".exe")
	switch s {
	case "zsh", "bash", "fish", "pwsh":
		return s
	case "powershell":
		return "pwsh"
	}
	return ""
}

func getenv(e *Env, k string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(k)
}

// run calls e.Exec with a bounded context; a nil Exec behaves like a missing command.
func run(e *Env, name string, args ...string) (string, error) {
	if e.Exec == nil {
		return "", errors.New("exec unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), execWait)
	defer cancel()
	return e.Exec(ctx, name, args...)
}

func zshrc(e *Env) string {
	dir := getenv(e, "ZDOTDIR")
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(e.Home, dir[2:])
	}
	if dir == "" {
		dir = e.Home
	}
	return filepath.Join(dir, ".zshrc")
}

func fishConfDir(e *Env) string {
	if x := getenv(e, "XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "fish")
	}
	return filepath.Join(e.Home, ".config", "fish")
}

// pwshProfiles asks each installed PowerShell edition for $PROFILE.CurrentUserAllHosts.
func pwshProfiles(e *Env) []string {
	var out []string
	for _, exe := range []string{"pwsh", "powershell"} {
		if e.LookPath == nil {
			continue
		}
		if _, err := e.LookPath(exe); err != nil {
			continue
		}
		s, err := run(e, exe, "-NoProfile", "-Command", "$PROFILE.CurrentUserAllHosts")
		p := strings.TrimSpace(strings.TrimPrefix(s, "\uFEFF"))
		if err != nil || !filepath.IsAbs(p) || contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// HookTargets lists the files the hook step writes for shell, with the block body or drop-in content.
func HookTargets(e *Env, sh string) ([]Target, error) {
	switch sh {
	case "zsh":
		return []Target{{zshrc(e), "block", hookLine(sh)}}, nil
	case "bash":
		t := []Target{{filepath.Join(e.Home, ".bashrc"), "block", hookLine(sh)}}
		bp := filepath.Join(e.Home, ".bash_profile")
		if e.GOOS == "darwin" && !bashProfileSourced(bp) {
			t = append(t, Target{bp, "block", bashProfileLine})
		}
		return t, nil
	case "fish":
		p := filepath.Join(fishConfDir(e), "conf.d", "oci-assume.fish")
		return []Target{{p, "dropin", fishHeader + hookLine(sh) + "\n"}}, nil
	case "pwsh":
		ps := pwshProfiles(e)
		if len(ps) == 0 {
			return nil, errors.New("no PowerShell found to ask for $PROFILE")
		}
		var t []Target
		for _, p := range ps {
			t = append(t, Target{p, "block", hookLine(sh)})
		}
		return t, nil
	}
	return nil, fmt.Errorf("unsupported shell %q", sh)
}

// bashProfileSourced reports whether .bash_profile exists and already sources .bashrc (our block counts).
func bashProfileSourced(p string) bool {
	b, err := os.ReadFile(p) // #nosec G304 -- fixed rc path under HOME
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "#") && sourcesBashrc.MatchString(l) {
			return true
		}
	}
	return false
}

// legacyFiles are where a hand-added, unmarked hook line may live for sh.
func legacyFiles(e *Env, sh string, targets []Target) []string {
	switch sh {
	case "zsh":
		return []string{zshrc(e)}
	case "bash":
		return []string{filepath.Join(e.Home, ".bashrc"), filepath.Join(e.Home, ".bash_profile")}
	case "fish":
		return []string{filepath.Join(fishConfDir(e), "config.fish")}
	}
	var out []string
	for _, t := range targets {
		out = append(out, t.Path, filepath.Join(filepath.Dir(t.Path), "Microsoft.PowerShell_profile.ps1"))
	}
	return out
}

// hasLegacyLine reports an uncommented `oci-assume init` line outside our markers.
func hasLegacyLine(p string) bool {
	b, err := os.ReadFile(p) // #nosec G304 -- fixed rc path under HOME
	if err != nil {
		return false
	}
	in := false
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == markBegin:
			in = true
		case l == markEnd:
			in = false
		case !in && !strings.HasPrefix(l, "#") && strings.Contains(l, "oci-assume init"):
			return true
		}
	}
	return false
}

// region locates our marked block: start of the begin line to the end of the end line (with its EOL).
func region(s string) (start, end int, found bool, err error) {
	off := 0
	start = -1
	for off < len(s) {
		n := strings.IndexByte(s[off:], '\n')
		next := len(s)
		if n >= 0 {
			next = off + n + 1
		}
		l := strings.TrimSpace(s[off:next])
		if start < 0 && l == markBegin {
			start = off
		} else if start >= 0 && l == markEnd {
			return start, next, true, nil
		}
		off = next
	}
	if start >= 0 {
		return 0, 0, false, errors.New("oci-assume block has no end marker; fix it by hand")
	}
	return 0, 0, false, nil
}

func eolOf(s, goos string) string {
	if strings.Contains(s, "\r\n") || (s == "" && goos == "windows") {
		return "\r\n"
	}
	return "\n"
}

func block(body, eol string) string {
	return markBegin + eol + strings.ReplaceAll(body, "\n", eol) + eol + markEnd + eol
}

// withBlock returns s with our block set to body: replaced in place, else appended after a separator EOL.
func withBlock(s, body, goos string) (string, error) {
	eol := eolOf(s, goos)
	b := block(body, eol)
	start, end, found, err := region(s)
	if err != nil {
		return "", err
	}
	if found {
		return s[:start] + b + s[end:], nil
	}
	if s == "" {
		return b, nil
	}
	return s + eol + b, nil
}

// withoutBlock removes our block; at EOF it also drops the separator EOL that withBlock added.
func withoutBlock(s string) (string, error) {
	start, end, found, err := region(s)
	if err != nil || !found {
		return s, err
	}
	out := s[:start] + s[end:]
	if end == len(s) {
		if strings.HasSuffix(out, "\r\n") {
			out = out[:len(out)-2]
		} else {
			out = strings.TrimSuffix(out, "\n")
		}
	}
	return out, nil
}

// resolve follows symlinks so dotfile managers keep their links; a missing file resolves to itself.
func resolve(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}
	if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a broken symlink: %w", p, err)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	return "", err
}

// readRC returns the resolved path, content, mode and whether the file exists.
func readRC(p string) (real, s string, mode fs.FileMode, exists bool, err error) {
	if real, err = resolve(p); err != nil {
		return "", "", 0, false, err
	}
	b, err := os.ReadFile(real) // #nosec G304 -- rc path from HookTargets or the receipt
	if errors.Is(err, fs.ErrNotExist) {
		return real, "", 0o644, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", "", 0, false, err
	}
	return real, string(b), fi.Mode().Perm(), true, nil
}

// writeRC backs up the old content to <path>.oci-assume.bak, then writes atomically with the old mode.
func writeRC(p, real, old, s string, mode fs.FileMode, exists bool) error {
	if exists {
		if mode&0o200 == 0 {
			return fmt.Errorf("%s is read-only; add the oci-assume block by hand or make it writable", p)
		}
		if err := os.WriteFile(p+bakSuffix, []byte(old), mode); err != nil { // #nosec G306 -- mirrors the rc's own mode
			return fmt.Errorf("back up %s: %w", p, err)
		}
	}
	return atomicWrite(real, s, mode)
}

func atomicWrite(p, s string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".oci-assume-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.WriteString(s)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(name, mode)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// targetDone reports whether t already holds exactly our content.
func targetDone(t Target) (bool, error) {
	_, s, _, exists, err := readRC(t.Path)
	if err != nil || !exists {
		return false, err
	}
	if t.Kind == "dropin" {
		return s == t.Content, nil
	}
	start, end, found, err := region(s)
	if err != nil || !found {
		return false, err
	}
	got := strings.ReplaceAll(s[start:end], "\r\n", "\n")
	return got == block(t.Content, "\n"), nil
}

// applyTarget writes one target; it is a no-op when the content is already current.
func applyTarget(e *Env, t Target) error {
	real, s, mode, exists, err := readRC(t.Path)
	if err != nil {
		return err
	}
	next := t.Content
	if t.Kind == "block" {
		if next, err = withBlock(s, t.Content, e.GOOS); err != nil {
			return fmt.Errorf("%s: %w", t.Path, err)
		}
	}
	if exists && next == s {
		return nil
	}
	if t.Kind == "dropin" {
		return atomicWrite(real, next, 0o644)
	}
	return writeRC(t.Path, real, s, next, mode, exists)
}

// undoFile removes our block (deleting a file left empty) or the whole drop-in.
func undoFile(f FileEntry) error {
	if f.Kind == "dropin" {
		err := os.Remove(f.Path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	real, s, mode, exists, err := readRC(f.Path)
	if err != nil || !exists {
		return err
	}
	next, err := withoutBlock(s)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	if next == s {
		return nil
	}
	// Left empty: delete it unless an empty backup shows the file pre-existed empty.
	if bak, berr := os.ReadFile(f.Path + bakSuffix); next == "" && (berr != nil || len(bak) > 0) { // #nosec G304 -- our own backup path
		if err := os.WriteFile(f.Path+bakSuffix, []byte(s), mode); err != nil { // #nosec G306 -- mirrors the rc's own mode
			return err
		}
		return os.Remove(real)
	}
	return writeRC(f.Path, real, s, next, mode, true)
}
