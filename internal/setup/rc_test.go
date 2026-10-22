package setup

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// hookEnv is a temp-HOME Env with a fixed environment and a scripted Exec.
func hookEnv(t *testing.T, goos string, env map[string]string, exec map[string]string) (*Env, *strings.Builder) {
	t.Helper()
	e, _ := testEnv(t, "", false)
	out := &strings.Builder{}
	e.GOOS, e.Out = goos, out
	e.Getenv = func(k string) string { return env[k] }
	e.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		if v, ok := exec[name+" "+strings.Join(args, " ")]; ok {
			return v, nil
		}
		return "", errors.New("not found: " + name)
	}
	e.LookPath = func(name string) (string, error) {
		for k := range exec {
			if strings.HasPrefix(k, name+" ") {
				return "/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
	return e, out
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// golden compares with LF endings, since .gitattributes stores goldens as LF; CRLF is asserted separately.
func golden(t *testing.T, name, got string) {
	t.Helper()
	got = strings.ReplaceAll(got, "\r\n", "\n")
	p := filepath.Join("testdata", "hook", name+".golden")
	if *update {
		write(t, p, got)
	}
	if want := read(t, p); got != want {
		t.Fatalf("%s mismatch:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func apply(t *testing.T, e *Env) *Receipt {
	t.Helper()
	r := &Receipt{}
	if err := NewHookStep().Apply(e, r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBlockZsh(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "export FOO=1\n")
	r := apply(t, e)
	golden(t, "zsh", read(t, rc))
	if len(r.Files) != 1 || r.Files[0] != (FileEntry{Path: rc, Kind: "block"}) {
		t.Fatalf("receipt %+v", r.Files)
	}
	if fi, _ := os.Stat(rc + ".oci-assume.bak"); fi == nil {
		t.Fatal("no backup")
	}
	if done, err := NewHookStep().Done(e); err != nil || !done {
		t.Fatalf("done %v %v", done, err)
	}
}

func TestBlockBash(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "bash"
	rc := filepath.Join(e.Home, ".bashrc")
	write(t, rc, "alias ll='ls -l'\n")
	apply(t, e)
	golden(t, "bash", read(t, rc))
	if _, err := os.Stat(filepath.Join(e.Home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatalf("bash_profile touched on linux: %v", err)
	}
}

func TestBlockFishDropin(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "fish"
	r := apply(t, e)
	p := filepath.Join(e.Home, ".config", "fish", "conf.d", "oci-assume.fish")
	golden(t, "fish", read(t, p))
	if len(r.Files) != 1 || r.Files[0] != (FileEntry{Path: p, Kind: "dropin"}) {
		t.Fatalf("receipt %+v", r.Files)
	}
	if err := NewHookStep().Undo(e, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("drop-in not removed: %v", err)
	}
}

func TestBlockPwshBothEditions(t *testing.T) {
	core := filepath.Join(t.TempDir(), "OneDrive", "Documents", "PowerShell", "profile.ps1")
	desk := filepath.Join(t.TempDir(), "Documents", "WindowsPowerShell", "profile.ps1")
	e, _ := hookEnv(t, "windows", nil, map[string]string{
		"pwsh -NoProfile -Command $PROFILE.CurrentUserAllHosts":       "\ufeff" + core + "\r\n",
		"powershell -NoProfile -Command $PROFILE.CurrentUserAllHosts": desk + "\r\n",
	})
	e.Shell = "pwsh"
	write(t, desk, "Set-Alias g git\r\n")
	r := apply(t, e)
	for name, p := range map[string]string{"pwsh-new": core, "pwsh-existing": desk} {
		got := read(t, p)
		if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
			t.Fatalf("%s: not CRLF: %q", name, got)
		}
		golden(t, name, got)
	}
	if len(r.Files) != 2 {
		t.Fatalf("receipt %+v", r.Files)
	}
}

func TestIdempotentTwiceOneBlock(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "a\n")
	r := apply(t, e)
	first := read(t, rc)
	if err := NewHookStep().Apply(e, r); err != nil {
		t.Fatal(err)
	}
	// A stale block body is replaced in place, not duplicated.
	write(t, rc, strings.Replace(first, "init zsh", "init old", 1)+"tail\n")
	if err := NewHookStep().Apply(e, r); err != nil {
		t.Fatal(err)
	}
	got := read(t, rc)
	if got != first+"tail\n" || strings.Count(got, markBegin) != 1 || len(r.Files) != 1 {
		t.Fatalf("got %q files %+v", got, r.Files)
	}
}

func TestBlockNoTrailingNewline(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "export A=1")
	apply(t, e)
	got := read(t, rc)
	if !strings.HasPrefix(got, "export A=1\n"+markBegin+"\n") {
		t.Fatalf("got %q", got)
	}
}

func TestCRLFPreserved(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "bash"
	rc := filepath.Join(e.Home, ".bashrc")
	write(t, rc, "a\r\nb\r\n")
	r := apply(t, e)
	got := read(t, rc)
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") || !strings.Contains(got, markEnd+"\r\n") {
		t.Fatalf("mixed EOL: %q", got)
	}
	if err := NewHookStep().Undo(e, r); err != nil {
		t.Fatal(err)
	}
	if got := read(t, rc); got != "a\r\nb\r\n" {
		t.Fatalf("after undo %q", got)
	}
}

func TestLegacyLineDetected(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	orig := "# my rc\neval \"$(oci-assume init zsh)\"\n"
	write(t, rc, orig)
	done, err := NewHookStep().Done(e)
	if err != nil || !done {
		t.Fatalf("done %v %v", done, err)
	}
	r := apply(t, e)
	if read(t, rc) != orig || len(r.Files) != 0 {
		t.Fatalf("legacy rc modified: %q %+v", read(t, rc), r.Files)
	}
	// A commented-out line is not a hook.
	write(t, rc, "# eval \"$(oci-assume init zsh)\"\n")
	if done, _ := NewHookStep().Done(e); done {
		t.Fatal("commented legacy line counted as done")
	}
}

func TestZDOTDIRHonoured(t *testing.T) {
	zd := t.TempDir()
	e, _ := hookEnv(t, "linux", map[string]string{"ZDOTDIR": zd}, nil)
	e.Shell = "zsh"
	apply(t, e)
	if !strings.Contains(read(t, filepath.Join(zd, ".zshrc")), markBegin) {
		t.Fatal("block not in $ZDOTDIR/.zshrc")
	}
	if _, err := os.Stat(filepath.Join(e.Home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf("~/.zshrc touched: %v", err)
	}
}

func TestMacBashProfileSourcesBashrc(t *testing.T) {
	e, _ := hookEnv(t, "darwin", nil, nil)
	e.Shell = "bash"
	bp := filepath.Join(e.Home, ".bash_profile")
	// Missing .bash_profile: created with a sourcing block.
	r := apply(t, e)
	golden(t, "bash_profile", read(t, bp))
	if len(r.Files) != 2 {
		t.Fatalf("receipt %+v", r.Files)
	}
	if err := NewHookStep().Undo(e, r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bp); !os.IsNotExist(err) {
		t.Fatalf("created bash_profile not removed: %v", err)
	}
	// An existing .bash_profile that already sources .bashrc is left alone.
	orig := "export PATH=/x:$PATH\n[ -r ~/.bashrc ] && . ~/.bashrc\n"
	write(t, bp, orig)
	tg, err := HookTargets(e, "bash")
	if err != nil || len(tg) != 1 {
		t.Fatalf("targets %+v %v", tg, err)
	}
	apply(t, e)
	if read(t, bp) != orig {
		t.Fatalf("bash_profile modified: %q", read(t, bp))
	}
}

func TestSymlinkedRCWritesTarget(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	real := filepath.Join(t.TempDir(), "dotfiles", "zshrc")
	write(t, real, "x\n")
	if err := os.Chmod(real, 0o640); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(e.Home, ".zshrc")
	if err := os.Symlink(real, rc); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := apply(t, e)
	if fi, err := os.Lstat(rc); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v", err)
	}
	if !strings.Contains(read(t, real), markBegin) {
		t.Fatal("target not written")
	}
	if fi, _ := os.Stat(real); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if err := NewHookStep().Undo(e, r); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(rc); fi.Mode()&os.ModeSymlink == 0 || read(t, real) != "x\n" {
		t.Fatalf("undo broke symlink or content %q", read(t, real))
	}
}

func TestUninstallKeepsUserEdits(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "a\n")
	r := apply(t, e)
	cur := read(t, rc)
	i, j := strings.Index(cur, markBegin), strings.Index(cur, markEnd)+len(markEnd)+1
	edited := "top\n" + cur[:i] + cur[i:j] + "after\n" + cur[j:] + "bottom"
	write(t, rc, edited)
	if err := NewHookStep().Undo(e, r); err != nil {
		t.Fatal(err)
	}
	want := "top\n" + cur[:i] + "after\n" + cur[j:] + "bottom"
	if got := read(t, rc); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if len(r.Files) != 0 {
		t.Fatalf("receipt not cleared: %+v", r.Files)
	}
}

func TestUninstallRestoresOriginalWhenOnlyChange(t *testing.T) {
	for name, orig := range map[string]string{"trailing": "a\nb\n", "none": "a\nb", "empty": ""} {
		t.Run(name, func(t *testing.T) {
			e, _ := hookEnv(t, "linux", nil, nil)
			e.Shell = "bash"
			rc := filepath.Join(e.Home, ".bashrc")
			write(t, rc, orig)
			r := apply(t, e)
			if err := NewHookStep().Undo(e, r); err != nil {
				t.Fatal(err)
			}
			if got := read(t, rc); got != orig {
				t.Fatalf("got %q want %q", got, orig)
			}
			// Twice is safe, and a receipt-less run still finds nothing to do.
			if err := NewHookStep().Undo(e, &Receipt{}); err != nil || read(t, rc) != orig {
				t.Fatalf("second undo %v", err)
			}
		})
	}
}

func TestUninstallWithoutReceiptScansKnownFiles(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "a\n")
	apply(t, e)
	e.Shell = ""
	if err := NewHookStep().Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, rc); got != "a\n" {
		t.Fatalf("got %q", got)
	}
}

func TestDetectShellOrder(t *testing.T) {
	ps := "ps -o comm= -p " + strconv.Itoa(os.Getppid())
	cases := []struct {
		name, goos, flag, parent, env, want string
	}{
		{"flag wins", "linux", "fish", "-zsh", "/bin/bash", "fish"},
		{"login parent", "darwin", "", "-zsh", "/bin/bash", "zsh"},
		{"parent path", "linux", "", "/usr/local/bin/fish", "/bin/bash", "fish"},
		{"sh parent falls to SHELL", "linux", "", "sh", "/opt/homebrew/bin/bash", "bash"},
		{"windows parent exe", "windows", "", "powershell.exe", "", "pwsh"},
		{"windows default", "windows", "", "cmd.exe", "", "pwsh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := hookEnv(t, c.goos, map[string]string{"SHELL": c.env}, map[string]string{ps: c.parent + "\n"})
			if c.goos == "windows" {
				old := windowsParent
				windowsParent = func() (string, error) { return c.parent, nil }
				defer func() { windowsParent = old }()
			}
			got, err := DetectShell(e, c.flag)
			if err != nil || got != c.want {
				t.Fatalf("got %q %v want %q", got, err, c.want)
			}
		})
	}
	e, _ := hookEnv(t, "linux", nil, nil)
	if _, err := DetectShell(e, "tcsh"); err == nil {
		t.Fatal("bad flag accepted")
	}
	if _, err := DetectShell(e, ""); !errors.Is(err, ErrUnknownShell) {
		t.Fatalf("err %v", err)
	}
}

func TestUnknownShellPrintsAllAndSkips(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	s := NewHookStep()
	if done, err := s.Done(e); err != nil || done {
		t.Fatalf("done %v %v", done, err)
	}
	d := s.Describe(e)
	for _, want := range []string{
		`eval "$(oci-assume init zsh)"`, `eval "$(oci-assume init bash)"`,
		"oci-assume init fish | source", "oci-assume init pwsh | Out-String | Invoke-Expression", "--shell",
	} {
		if !strings.Contains(d, want) {
			t.Fatalf("describe missing %q:\n%s", want, d)
		}
	}
	r := apply(t, e)
	ents, _ := os.ReadDir(e.Home)
	if len(r.Files) != 0 || len(ents) != 0 {
		t.Fatalf("wrote files: %+v %v", r.Files, ents)
	}
}

func TestReadOnlyRCRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "a\n")
	if err := os.Chmod(rc, 0o444); err != nil {
		t.Fatal(err)
	}
	err := NewHookStep().Apply(e, &Receipt{})
	if err == nil || !strings.Contains(err.Error(), "read-only") || read(t, rc) != "a\n" {
		t.Fatalf("err %v", err)
	}
}
