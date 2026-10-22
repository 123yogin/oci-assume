package refresher

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

const (
	testBin  = "/usr/local/bin/oci-assume"
	testPath = "/usr/bin:/bin"
)

// setCaptured sets the install-time variables a scheduled run inherits.
func setCaptured(t *testing.T, cfg, stateDir, ociBin string) {
	t.Setenv("OCI_CLI_CONFIG_FILE", cfg)
	t.Setenv("OCI_ASSUME_STATE_DIR", stateDir)
	t.Setenv("OCI_ASSUME_OCI_BIN", ociBin)
}

func TestRenderGolden(t *testing.T) {
	t.Setenv("PATH", testPath)
	t.Setenv("HOME", "/home/user")
	t.Setenv("USERPROFILE", "/home/user")
	setCaptured(t, "/home/user/.oci/config", "/home/user/state", "/opt/oci/bin/oci")
	for _, goos := range []string{"darwin", "linux", "windows"} {
		files, err := Render(goos, testBin)
		if err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			golden := filepath.Join("testdata", "golden", goos, name)
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(files[name]), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%s: %v (run with -update)", golden, err)
			}
			if files[name] != strings.ReplaceAll(string(want), "\r\n", "\n") {
				t.Errorf("%s mismatch:\n--- got\n%s\n--- want\n%s", golden, files[name], want)
			}
		}
	}
	if _, err := Render("plan9", testBin); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plan9: %v", err)
	}
}

func TestRenderEscapes(t *testing.T) {
	t.Setenv("PATH", `/a b:/c"d%e$f`)
	t.Setenv("HOME", "/home/user")
	t.Setenv("USERPROFILE", "/home/user")
	setCaptured(t, "", "", "")
	bin := `/x & y/o<a>$`
	f, _ := Render("darwin", bin)
	if !strings.Contains(f[Label+".plist"], "<string>/x &amp; y/o&lt;a&gt;$</string>") {
		t.Errorf("plist not escaped:\n%s", f[Label+".plist"])
	}
	f, _ = Render("linux", bin)
	if !strings.Contains(f[serviceName], `Environment="PATH=/a b:/c\"d%%e$f"`) ||
		!strings.Contains(f[serviceName], `ExecStart="/x & y/o<a>$$" refresh --all --background`) {
		t.Errorf("service not escaped:\n%s", f[serviceName])
	}
	if want := `*/30 * * * * PATH='/a b:/c"d\%e$f' '/x & y/o<a>$' refresh --all --background >/dev/null 2>&1 # oci-assume` + "\n"; f["crontab"] != want {
		t.Errorf("cron line %q", f["crontab"])
	}
	f, _ = Render("windows", bin)
	if !strings.Contains(f["task.xml"], "<Command>/x &amp; y/o&lt;a&gt;$</Command>") {
		t.Errorf("task.xml not escaped")
	}
}

// I3: unset captured variables add nothing; set ones are escaped like PATH.
func TestRenderCapturedEnv(t *testing.T) {
	t.Setenv("PATH", testPath)
	t.Setenv("HOME", "/home/user")
	t.Setenv("USERPROFILE", "/home/user")
	setCaptured(t, "", "", "")
	for _, goos := range []string{"darwin", "linux", "windows"} {
		f, err := Render(goos, testBin)
		if err != nil {
			t.Fatal(err)
		}
		for n, body := range f {
			for _, k := range captured {
				if strings.Contains(body, k) {
					t.Errorf("%s/%s mentions unset %s:\n%s", goos, n, k, body)
				}
			}
		}
	}
	setCaptured(t, "/c fg/%x$y&z", "", "")
	f, _ := Render("darwin", testBin)
	if !strings.Contains(f[Label+".plist"], "<key>OCI_CLI_CONFIG_FILE</key>\n\t\t<string>/c fg/%x$y&amp;z</string>") {
		t.Errorf("plist:\n%s", f[Label+".plist"])
	}
	f, _ = Render("linux", testBin)
	if !strings.Contains(f[serviceName], `Environment="OCI_CLI_CONFIG_FILE=/c fg/%%x$y&z"`) {
		t.Errorf("service:\n%s", f[serviceName])
	}
	if want := `*/30 * * * * PATH=/usr/bin:/bin OCI_CLI_CONFIG_FILE='/c fg/\%x$y&z' /usr/local/bin/oci-assume refresh --all --background >/dev/null 2>&1 # oci-assume` + "\n"; f["crontab"] != want {
		t.Errorf("cron %q", f["crontab"])
	}
	setCaptured(t, `C:\Users\a b\cfg`, `C:\state\`, "")
	f, _ = Render("windows", testBin)
	if !strings.Contains(f["task.xml"], `<Arguments>refresh --all --background --env &#34;OCI_ASSUME_STATE_DIR=C:\state\\&#34; --env &#34;OCI_CLI_CONFIG_FILE=C:\Users\a b\cfg&#34;</Arguments>`) {
		t.Errorf("task.xml:\n%s", f["task.xml"])
	}
	setCaptured(t, "", `C:\a"b`, "")
	if _, err := Render("windows", testBin); err == nil {
		t.Error("quote in a windows value accepted")
	}
}

func TestBackendsCaptureEnv(t *testing.T) {
	f := &fakeExec{rules: []rule{{"launchctl print", "", errExit}, {"systemctl", "", errExit}}}
	s := testSys(t, f, nil)
	s.getenv = func(k string) string {
		return map[string]string{"PATH": testPath, "OCI_ASSUME_STATE_DIR": "/st"}[k]
	}
	if err := (&launchd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.home, "Library", "LaunchAgents", Label+".plist"))
	if !strings.Contains(string(b), "<string>/st</string>") {
		t.Fatalf("plist:\n%s", b)
	}
	if err := (&systemd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	if got := f.stdins[len(f.stdins)-1]; !strings.Contains(got, "PATH=/usr/bin:/bin OCI_ASSUME_STATE_DIR=/st ") {
		t.Fatalf("cron %q", got)
	}
	s.getenv = func(k string) string { return map[string]string{"PATH": testPath, "OCI_CLI_CONFIG_FILE": "/c\n"}[k] }
	if err := (&systemd{s}).Install(testBin); err == nil || !strings.Contains(err.Error(), "control characters") {
		t.Fatalf("cron accepted a control char: %v", err)
	}
}

func mkExe(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// C2: Homebrew casks live under Caskroom/<name>/<version>/.
func TestCaskroomPathStabilized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(root, "Caskroom", "oci-assume", "1.0.0", "oci-assume")
	mkExe(t, real)
	link := filepath.Join(root, "bin", "oci-assume")
	symlink(t, real, link)
	if got := stableBin(real); got != link {
		t.Fatalf("got %s, want %s", got, link)
	}
}

// C2: a PATH entry resolving to the running binary wins, unresolved.
func TestBinPathPrefersLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(root, "opt", "pkg", "1.0", "oci-assume")
	mkExe(t, real)
	link := filepath.Join(root, "bin", "oci-assume")
	symlink(t, real, link)
	look := func(string) (string, error) { return link, nil }
	if got, err := binPathFrom(real, look); err != nil || got != link {
		t.Fatalf("got %s %v, want %s", got, err, link)
	}
	other := filepath.Join(root, "other", "oci-assume")
	mkExe(t, other)
	look = func(string) (string, error) { return other, nil }
	if got, _ := binPathFrom(real, look); got != real {
		t.Fatalf("different PATH binary used: %s", got)
	}
	look = func(string) (string, error) { return "", exec.ErrNotFound }
	if got, _ := binPathFrom(link, look); got != real {
		t.Fatalf("no PATH entry: got %s, want resolved %s", got, real)
	}
}

func TestCellarPathStabilized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root := t.TempDir()
	real := filepath.Join(root, "Cellar", "oci-assume", "1.0.0", "bin", "oci-assume")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := stableBin(real); got != real {
		t.Fatalf("no symlink yet: got %s", got)
	}
	link := filepath.Join(root, "bin", "oci-assume")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got := stableBin(real); got != link {
		t.Fatalf("got %s, want %s", got, link)
	}
	// A prefix symlink to a different binary is not used.
	other := filepath.Join(root, "Cellar", "oci-assume", "0.9.0", "bin", "oci-assume")
	if got := stableBin(other); got != other {
		t.Fatalf("mismatched link used: %s", got)
	}
	if got := stableBin("/usr/local/bin/oci-assume"); got != "/usr/local/bin/oci-assume" {
		t.Fatalf("non-cellar changed: %s", got)
	}
}

type rule struct {
	prefix string
	out    string
	err    error
}

type fakeExec struct {
	calls  []string
	stdins []string
	rules  []rule
}

func (f *fakeExec) run(stdin, name string, args ...string) (string, error) {
	c := strings.Join(append([]string{name}, args...), " ")
	f.calls = append(f.calls, c)
	f.stdins = append(f.stdins, stdin)
	for _, r := range f.rules {
		if strings.HasPrefix(c, r.prefix) {
			return r.out, r.err
		}
	}
	return "", nil
}

var errExit = errors.New("exit status 1")

func testSys(t *testing.T, f *fakeExec, results map[string]state.Result) *sys {
	home := t.TempDir()
	return &sys{
		exec:      f.run,
		home:      home,
		configDir: filepath.Join(home, ".config"),
		tmpDir:    t.TempDir(),
		uid:       501,
		getenv:    func(k string) string { return map[string]string{"PATH": testPath}[k] },
		results:   func() (map[string]state.Result, error) { return results, nil },
	}
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLaunchdInstallFresh(t *testing.T) {
	f := &fakeExec{rules: []rule{{"launchctl print", "", errExit}}}
	s := testSys(t, f, nil)
	if err := (&launchd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(s.home, "Library", "LaunchAgents", Label+".plist")
	eq(t, f.calls, []string{
		"launchctl print gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	})
	b, err := os.ReadFile(plist)
	if err != nil || !strings.Contains(string(b), "<string>"+testPath+"</string>") ||
		!strings.Contains(string(b), logPath(s.home)) {
		t.Fatalf("plist %v:\n%s", err, b)
	}
}

func TestLaunchdReinstallBootsOutFirst(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	if err := (&launchd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(s.home, "Library", "LaunchAgents", Label+".plist")
	eq(t, f.calls, []string{
		"launchctl print gui/501/" + Label,
		"launchctl bootout gui/501/" + Label,
		"launchctl bootstrap gui/501 " + plist,
	})
}

func TestLaunchdUninstall(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	l := &launchd{s}
	if err := l.Install(testBin); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := l.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"launchctl print gui/501/" + Label, "launchctl bootout gui/501/" + Label})
	if _, err := os.Stat(filepath.Join(s.home, "Library", "LaunchAgents", Label+".plist")); !os.IsNotExist(err) {
		t.Fatalf("plist left: %v", err)
	}
	// Not loaded and no plist is fine.
	f.calls, f.rules = nil, []rule{{"launchctl print", "", errExit}}
	if err := l.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"launchctl print gui/501/" + Label})
}

func TestLaunchdStatus(t *testing.T) {
	at := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	f := &fakeExec{}
	s := testSys(t, f, map[string]state.Result{
		"dev":        {At: at, OK: true, Background: true},
		"prod-admin": {At: at.Add(-time.Hour), OK: false, Msg: "x", Background: true},
		"manual":     {At: at.Add(time.Hour), OK: true},
	})
	l := &launchd{s}
	st, err := l.Status()
	if err != nil || st.Installed {
		t.Fatalf("before install: %+v %v", st, err)
	}
	if err := l.Install(testBin); err != nil {
		t.Fatal(err)
	}
	st, err = l.Status()
	want := Status{Installed: true, Path: filepath.Join(s.home, "Library", "LaunchAgents", Label+".plist"), LastRun: at, LastExit: 1}
	if err != nil || st != want {
		t.Fatalf("got %+v %v, want %+v", st, err, want)
	}
	s.results = func() (map[string]state.Result, error) {
		return map[string]state.Result{"dev": {At: at, OK: true, Background: true}, "manual": {At: at, OK: false}}, nil
	}
	if st, _ = l.Status(); st.LastExit != 0 {
		t.Fatalf("all OK should be exit 0: %+v", st)
	}
	s.results = func() (map[string]state.Result, error) { return nil, errors.New("bad json") }
	if st, err = l.Status(); !errors.Is(err, ErrLastRun) || !st.Installed {
		t.Fatalf("state error: %+v %v", st, err)
	}
	// Plist present but not loaded is not installed.
	f.rules = []rule{{"launchctl print", "", errExit}}
	if st, _ = l.Status(); st.Installed {
		t.Fatalf("unloaded reported installed")
	}
}

func TestSystemdInstall(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	if err := (&systemd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now " + timerName,
		"crontab -l",
	})
	dir := filepath.Join(s.configDir, "systemd", "user")
	b, err := os.ReadFile(filepath.Join(dir, serviceName))
	if err != nil || !strings.Contains(string(b), `Environment="PATH=`+testPath+`"`) {
		t.Fatalf("service %v:\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, timerName)); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdUninstall(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	sd := &systemd{s}
	if err := sd.Install(testBin); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{
		"systemctl --user show-environment",
		"systemctl --user disable --now " + timerName,
		"systemctl --user daemon-reload",
		"crontab -l",
	})
	for _, n := range []string{serviceName, timerName} {
		if _, err := os.Stat(filepath.Join(s.configDir, "systemd", "user", n)); !os.IsNotExist(err) {
			t.Fatalf("%s left", n)
		}
	}
	f.calls = nil
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
}

func TestSystemdStatus(t *testing.T) {
	f := &fakeExec{rules: []rule{{"systemctl --user is-enabled", "disabled", errExit}}}
	s := testSys(t, f, nil)
	sd := &systemd{s}
	if st, err := sd.Status(); err != nil || st.Installed {
		t.Fatalf("%+v %v", st, err)
	}
	if err := sd.Install(testBin); err != nil {
		t.Fatal(err)
	}
	f.rules = nil
	st, err := sd.Status()
	if err != nil || !st.Installed || st.Path != filepath.Join(s.configDir, "systemd", "user", timerName) {
		t.Fatalf("%+v %v", st, err)
	}
}

const otherCron = "0 3 * * * /usr/bin/backup\n# keep me\n"

func TestCronFallbackInstall(t *testing.T) {
	old := "*/30 * * * * PATH=/old /old/oci-assume refresh --all --background # oci-assume\n"
	f := &fakeExec{rules: []rule{
		{"systemctl --user show-environment", "Failed to connect to bus", errExit},
		{"crontab -l", otherCron + old, nil},
	}}
	s := testSys(t, f, nil)
	if err := (&systemd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l", "crontab -"})
	want := otherCron + "*/30 * * * * PATH=/usr/bin:/bin /usr/local/bin/oci-assume refresh --all --background >/dev/null 2>&1 # oci-assume\n"
	if f.stdins[2] != want {
		t.Fatalf("crontab:\n%q\nwant\n%q", f.stdins[2], want)
	}
	if _, err := os.Stat(filepath.Join(s.configDir, "systemd")); !os.IsNotExist(err) {
		t.Fatal("unit files written in cron mode")
	}
}

func TestCronFallbackNoExistingCrontab(t *testing.T) {
	f := &fakeExec{rules: []rule{
		{"systemctl", "", errExit},
		{"crontab -l", "", errors.New("crontab -l: exit status 1: crontab: no crontab for user")},
	}}
	s := testSys(t, f, nil)
	if err := (&systemd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	if want := "*/30 * * * * PATH=/usr/bin:/bin /usr/local/bin/oci-assume refresh --all --background >/dev/null 2>&1 # oci-assume\n"; f.stdins[2] != want {
		t.Fatalf("got %q", f.stdins[2])
	}
	// A missing crontab binary is an error, not an empty table.
	f2 := &fakeExec{rules: []rule{{"systemctl", "", errExit}, {"crontab", "", fmt.Errorf("crontab: %w", exec.ErrNotFound)}}}
	if err := (&systemd{testSys(t, f2, nil)}).Install(testBin); err == nil {
		t.Fatal("want error")
	}
}

func TestCronFallbackUninstallKeepsOthers(t *testing.T) {
	line := "*/30 * * * * PATH=/usr/bin:/bin /usr/local/bin/oci-assume refresh --all --background # oci-assume\n"
	f := &fakeExec{rules: []rule{{"systemctl", "", errExit}, {"crontab -l", otherCron + line, nil}}}
	sd := &systemd{testSys(t, f, nil)}
	st, err := sd.Status()
	if err != nil || !st.Installed || st.Path != "crontab" {
		t.Fatalf("status %+v %v", st, err)
	}
	f.calls, f.stdins = nil, nil
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l", "crontab -"})
	if f.stdins[2] != otherCron {
		t.Fatalf("got %q", f.stdins[1])
	}
	// No marker: nothing is rewritten.
	f.calls, f.rules = nil, []rule{{"systemctl", "", errExit}, {"crontab -l", otherCron, nil}}
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
	// busybox wording and a missing binary both mean nothing to remove.
	for _, e := range []error{errors.New("crontab: can't open 'user': No such file"), fmt.Errorf("x: %w", exec.ErrNotFound)} {
		f.calls, f.rules = nil, []rule{{"crontab -l", "", e}}
		if err := sd.Uninstall(); err != nil {
			t.Fatalf("%v: %v", e, err)
		}
		eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
	}
}

func TestCronReadErrorNotOverwritten(t *testing.T) {
	f := &fakeExec{rules: []rule{{"crontab -l", "", errors.New("crontab: permission denied")}}}
	if err := (&systemd{testSys(t, f, nil)}).Uninstall(); err == nil {
		t.Fatal("want error")
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
}

// busybox "can't open" without "no such file" (e.g. permission denied) must never clobber the table.
func TestCronCantOpenPermissionDeniedNotOverwritten(t *testing.T) {
	f := &fakeExec{rules: []rule{
		{"systemctl", "", errExit},
		{"crontab -l", "", errors.New("crontab: can't open 'user': Permission denied")},
	}}
	if err := (&systemd{testSys(t, f, nil)}).Install(testBin); err == nil {
		t.Fatal("want error")
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
}

func TestCronEmptyTableNoBlankLine(t *testing.T) {
	f := &fakeExec{rules: []rule{{"systemctl", "", errExit}, {"crontab -l", "\n", nil}}}
	if err := (&systemd{testSys(t, f, nil)}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	if want := cronLine(testBin, testPath, nil) + "\n"; f.stdins[2] != want {
		t.Fatalf("got %q", f.stdins[2])
	}
}

// Installed via cron, uninstalled once systemd is available: the cron line still goes.
func TestUninstallCronWhileSystemdAvailable(t *testing.T) {
	f := &fakeExec{rules: []rule{{"crontab -l", otherCron + cronLine(testBin, testPath, nil) + "\n", nil}}}
	if err := (&systemd{testSys(t, f, nil)}).Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l", "crontab -"})
	if f.stdins[2] != otherCron {
		t.Fatalf("got %q", f.stdins[1])
	}
}

// Installed via systemd, uninstalled once systemctl is unavailable: unit files still go.
func TestUninstallUnitsWhileSystemdUnavailable(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	sd := &systemd{s}
	if err := sd.Install(testBin); err != nil {
		t.Fatal(err)
	}
	f.calls, f.rules = nil, []rule{{"systemctl", "", errExit}}
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"systemctl --user show-environment", "crontab -l"})
	for _, n := range []string{serviceName, timerName} {
		if exists(filepath.Join(s.configDir, "systemd", "user", n)) {
			t.Fatalf("%s left", n)
		}
	}
}

func TestSystemdInstallStripsStaleCron(t *testing.T) {
	f := &fakeExec{rules: []rule{{"crontab -l", otherCron + cronLine("/old/oci-assume", "/old", nil) + "\n", nil}}}
	if err := (&systemd{testSys(t, f, nil)}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	if f.calls[len(f.calls)-1] != "crontab -" || f.stdins[len(f.stdins)-1] != otherCron {
		t.Fatalf("calls %v stdin %q", f.calls, f.stdins[len(f.stdins)-1])
	}
}

func TestRenderRejectsControlChars(t *testing.T) {
	for _, c := range [][3]string{{"/bin/oa\n", testPath, ""}, {testBin, "/usr/bin\x00:/bin", ""}, {testBin, testPath, "/s\r"}} {
		env := []envVar{{"OCI_ASSUME_STATE_DIR", c[2]}}
		if _, err := render("linux", c[0], c[1], "/home/user", env); err == nil || !strings.Contains(err.Error(), "control characters") {
			t.Fatalf("%q: %v", c, err)
		}
	}
}

func TestSchtasks(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	w := &schtasks{s}
	if err := w.Install(testBin); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.HasPrefix(f.calls[0], "schtasks /Create /TN "+TaskName+" /XML "+s.tmpDir) ||
		!strings.HasSuffix(f.calls[0], " /F") {
		t.Fatalf("calls %v", f.calls)
	}
	if left, _ := os.ReadDir(s.tmpDir); len(left) != 0 {
		t.Fatalf("temp xml left behind")
	}
	f.calls = nil
	if err := w.Uninstall(); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls, []string{"schtasks /Query /TN " + TaskName, "schtasks /Delete /TN " + TaskName + " /F"})
	f.calls, f.rules = nil, []rule{{"schtasks /Query", "", errExit}}
	if err := w.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if st, _ := w.Status(); st.Installed {
		t.Fatal("installed")
	}
	f.rules = nil
	if st, _ := w.Status(); !st.Installed || st.Path != `\`+TaskName {
		t.Fatalf("%+v", st)
	}
}

func TestUTF16(t *testing.T) {
	if got := utf16le("a<"); !reflect.DeepEqual(got, []byte{0xFF, 0xFE, 'a', 0, '<', 0}) {
		t.Fatalf("%v", got)
	}
}

func TestForOS(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		if _, err := ForOS(goos); err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
	}
	if _, err := ForOS("plan9"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plan9: %v", err)
	}
}

// The unit dir comes from the systemd manager's environment, not the shell's.
func TestSystemdUnitsFollowManagerXDG(t *testing.T) {
	x := t.TempDir()
	f := &fakeExec{rules: []rule{{"systemctl --user show-environment", "HOME=/h\nXDG_CONFIG_HOME=" + x + "\n", nil}}}
	s := testSys(t, f, nil)
	if err := (&systemd{s}).Install(testBin); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(x, "systemd", "user", timerName)) {
		t.Fatalf("timer not under manager XDG %s", x)
	}
	if exists(filepath.Join(s.home, ".config", "systemd")) {
		t.Fatal("units also written under $HOME/.config")
	}
}

func TestSystemdUnitsIgnoreShellXDG(t *testing.T) {
	shell := t.TempDir()
	f := &fakeExec{rules: []rule{{"systemctl --user show-environment", "HOME=/h\nPATH=/bin\n", nil}}}
	s := testSys(t, f, nil)
	s.configDir = shell
	s.getenv = func(k string) string { return map[string]string{"PATH": testPath, "XDG_CONFIG_HOME": shell}[k] }
	sd := &systemd{s}
	if err := sd.Install(testBin); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(s.home, ".config", "systemd", "user", timerName)) {
		t.Fatal("timer not under $HOME/.config")
	}
	if exists(filepath.Join(shell, "systemd")) {
		t.Fatal("units written under the shell XDG_CONFIG_HOME")
	}
	if st, err := sd.Status(); err != nil || st.Path != filepath.Join(s.home, ".config", "systemd", "user", timerName) {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestSystemdUninstallUsesManagerXDG(t *testing.T) {
	x := t.TempDir()
	f := &fakeExec{rules: []rule{{"systemctl --user show-environment", "XDG_CONFIG_HOME=" + x + "\n", nil}}}
	sd := &systemd{testSys(t, f, nil)}
	if err := sd.Install(testBin); err != nil {
		t.Fatal(err)
	}
	if err := sd.Uninstall(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{serviceName, timerName} {
		if exists(filepath.Join(x, "systemd", "user", n)) {
			t.Fatalf("%s left", n)
		}
	}
}
