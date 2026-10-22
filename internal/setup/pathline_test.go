package setup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withBinary makes e's running binary dir/oci-assume; onPath is what LookPath("oci-assume") returns ("" = not found).
func withBinary(t *testing.T, e *Env, dir, onPath string) string {
	t.Helper()
	exe := filepath.Join(dir, "oci-assume")
	write(t, exe, "bin")
	e.Executable = func() (string, error) { return exe, nil }
	prev := e.LookPath
	e.LookPath = func(name string) (string, error) {
		if name == "oci-assume" {
			if onPath == "" {
				return "", errors.New("not found")
			}
			return onPath, nil
		}
		return prev(name)
	}
	return exe
}

func TestHookPathLineZshUnderHome(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	withBinary(t, e, filepath.Join(e.Home, ".local", "bin"), "")
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "export FOO=1\n")
	if d := NewHookStep().Describe(e); !strings.Contains(d, `export PATH="$HOME/.local/bin:$PATH"`+"\n    command -v oci-assume") {
		t.Fatalf("describe %q", d)
	}
	apply(t, e)
	golden(t, "zsh-path", read(t, rc))
	if done, err := NewHookStep().Done(e); !done || err != nil {
		t.Fatalf("done %v %v", done, err)
	}
}

func TestHookPathLineOutsideHomeEscaped(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "bash"
	// `"` is not a legal Windows file-name character; there use the other shell specials.
	name := `a $b"c`
	if runtime.GOOS == "windows" {
		name = "a $b`c'd&e;f"
	}
	dir := filepath.Join(t.TempDir(), name)
	withBinary(t, e, dir, "")
	apply(t, e)
	want := `export PATH="` + strings.NewReplacer(`\`, `\\`, `$`, `\$`, `"`, `\"`, "`", "\\`").Replace(dir) + `:$PATH"`
	if got := read(t, filepath.Join(e.Home, ".bashrc")); !strings.Contains(got, markBegin+"\n"+want+"\n"+hookLine("bash")+"\n") {
		t.Fatalf("got %q want line %q", got, want)
	}
}

func TestHookPathLineFish(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "fish"
	withBinary(t, e, filepath.Join(e.Home, ".local", "bin"), "")
	apply(t, e)
	golden(t, "fish-path", read(t, filepath.Join(e.Home, ".config", "fish", "conf.d", "oci-assume.fish")))
}

func TestHookPathLinePwsh(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "profile.ps1")
	e, _ := hookEnv(t, "windows", nil, map[string]string{"powershell " + pwshArgs: prof + "\r\n"})
	e.Shell = "pwsh"
	dir := filepath.Join(t.TempDir(), "bin")
	withBinary(t, e, dir, "")
	apply(t, e)
	want := `$env:Path = "` + dir + `;" + $env:Path` + "\r\n" + hookLine("pwsh")
	if got := read(t, prof); !strings.Contains(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestHookNoPathLineWhenResolvesToSelf(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	dir := t.TempDir()
	exe := withBinary(t, e, dir, "")
	link := filepath.Join(t.TempDir(), "oci-assume")
	if err := os.Symlink(exe, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	withBinary(t, e, dir, link)
	apply(t, e)
	if got := read(t, filepath.Join(e.Home, ".zshrc")); strings.Contains(got, "PATH") {
		t.Fatalf("PATH line added: %q", got)
	}
}

func TestHookPathLineWhenPathFindsOtherBinary(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	other := filepath.Join(t.TempDir(), "oci-assume")
	write(t, other, "old")
	withBinary(t, e, t.TempDir(), other)
	apply(t, e)
	if got := read(t, filepath.Join(e.Home, ".zshrc")); !strings.Contains(got, "export PATH=") {
		t.Fatalf("no PATH line: %q", got)
	}
}

func TestHookNoPathLineForOtherBinaryName(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	e.Executable = func() (string, error) { return "/tmp/go-build/setup.test", nil }
	apply(t, e)
	if got := read(t, filepath.Join(e.Home, ".zshrc")); strings.Contains(got, "PATH") {
		t.Fatalf("PATH line for a non-oci-assume binary: %q", got)
	}
}

func TestHookBlockMissingPathLineUpdatedInPlace(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	e.Shell = "zsh"
	rc := filepath.Join(e.Home, ".zshrc")
	write(t, rc, "a\n")
	apply(t, e)
	write(t, rc, read(t, rc)+"tail\n")
	withBinary(t, e, filepath.Join(e.Home, "bin"), "")
	if done, _ := NewHookStep().Done(e); done {
		t.Fatal("block without the needed PATH line counted as done")
	}
	apply(t, e)
	want := "a\n\n" + block(`export PATH="$HOME/bin:$PATH"`+"\n"+hookLine("zsh"), "\n") + "tail\n"
	if got := read(t, rc); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// Once the new shell puts it on PATH, the existing PATH line still counts as done.
	withBinary(t, e, filepath.Join(e.Home, "bin"), filepath.Join(e.Home, "bin", "oci-assume"))
	if done, _ := NewHookStep().Done(e); !done {
		t.Fatal("PATH line flip-flops once the binary resolves")
	}
}

func TestRunWarnsWhenStillNotOnPath(t *testing.T) {
	e, out := hookEnv(t, "linux", nil, nil)
	e.Shell, e.Interactive = "zsh", true
	dir := filepath.Join(e.Home, ".local", "bin")
	withBinary(t, e, dir, "")
	warn := "oci-assume is not on PATH in new shells: add " + dir + " to PATH\n"
	if _, err := Run(e, []Step{NewHookStep()}, Options{Yes: true, Skip: map[string]bool{"shell-hook": true}}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), warn) {
		t.Fatalf("output %q", out.String())
	}
	out.Reset()
	if _, err := Run(e, []Step{NewHookStep()}, Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not on PATH") {
		t.Fatalf("warned although the hook adds PATH: %q", out.String())
	}
}

func TestHookPathLinePwshSmartQuotesEscaped(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "profile.ps1")
	e, _ := hookEnv(t, "windows", nil, map[string]string{"powershell " + pwshArgs: prof + "\r\n"})
	e.Shell = "pwsh"
	dir := filepath.Join(t.TempDir(), "x“;Write-Host PWNED;“”„")
	withBinary(t, e, dir, "")
	apply(t, e)
	line := strings.SplitN(read(t, prof), "\r\n", 3)[1]
	if !strings.HasPrefix(line, "$env:Path = ") || !strings.Contains(line, "x`“;Write-Host PWNED;`“`”`„") {
		t.Fatalf("unescaped smart quotes: %q", line)
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not installed")
	}
	// Run only the PATH line; it must set PATH to dir and print nothing.
	out, err := exec.Command(pwsh, "-NoProfile", "-Command", "$env:Path = ''; "+line+"; [Console]::Out.Write($env:Path)").CombinedOutput()
	if err != nil || strings.Contains(string(out), "PWNED\n") || string(out) != dir+";" {
		t.Fatalf("pwsh ran %q: %q %v", line, out, err)
	}
}

func TestHookNoPathLineForControlChars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("newlines are not legal in Windows file names")
	}
	for _, sh := range []string{"bash", "fish"} {
		e, out := hookEnv(t, "linux", nil, nil)
		e.Shell, e.Interactive = sh, true
		dir := filepath.Join(t.TempDir(), "a\n# <<< oci-assume <<<\nb")
		withBinary(t, e, dir, "")
		if _, err := Run(e, []Step{NewHookStep()}, Options{Yes: true}); err != nil {
			t.Fatal(err)
		}
		rc := filepath.Join(e.Home, ".bashrc")
		if sh == "fish" {
			rc = filepath.Join(e.Home, ".config", "fish", "conf.d", "oci-assume.fish")
		}
		if got := read(t, rc); strings.Contains(got, "PATH") || strings.Contains(got, "fish_add_path") {
			t.Fatalf("%s: PATH line for a control-char dir: %q", sh, got)
		}
		if !strings.Contains(out.String(), "not on PATH in new shells") {
			t.Fatalf("%s: no PATH warning: %q", sh, out.String())
		}
	}
}

func TestBinDirScoopShimCountsAsOnPath(t *testing.T) {
	e, _ := hookEnv(t, "windows", nil, nil)
	shim := filepath.Join(t.TempDir(), "scoop", "shims", "oci-assume.exe")
	withBinary(t, e, filepath.Join(t.TempDir(), "scoop", "apps", "oci-assume", "current"), shim)
	if _, needed := BinDir(e); needed {
		t.Fatal("scoop shim treated as not on PATH")
	}
	withBinary(t, e, t.TempDir(), filepath.Join(t.TempDir(), "other", "shims", "oci-assume.exe"))
	if _, needed := BinDir(e); !needed {
		t.Fatal("non-scoop shims dir treated as on PATH")
	}
}

// winSession fakes a Windows session: onSession is LookPath's view, registry the PATH a new shell gets.
func winSession(e *Env, exe string, onSession bool, registry string) {
	e.Executable = func() (string, error) { return exe, nil }
	e.LookPath = func(name string) (string, error) {
		if name == "oci-assume" && onSession {
			return exe, nil
		}
		if name == "pwsh" || name == "powershell" {
			return `C:\bin\` + name + ".exe", nil
		}
		return "", errors.New("not found")
	}
	e.NewShellPath = func() (string, error) { return registry, nil }
}

func TestHookPwshStableAcrossInstallerAndNewSession(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "profile.ps1")
	e, _ := hookEnv(t, "windows", nil, map[string]string{"pwsh " + pwshArgs: prof + "\r\n"})
	e.Shell = "pwsh"
	dir := filepath.Join(t.TempDir(), "Programs", "oci-assume")
	exe := filepath.Join(dir, "oci-assume.exe")
	write(t, exe, "bin")
	reg := `C:\Windows\system32;` + dir
	// install.ps1 put dir in the user PATH and its own session; a later session only has the registry.
	winSession(e, exe, true, reg)
	apply(t, e)
	first := read(t, prof)
	if strings.Contains(first, "$env:Path") {
		t.Fatalf("PATH line although the user PATH has the dir: %q", first)
	}
	winSession(e, exe, false, reg)
	if done, err := NewHookStep().Done(e); !done || err != nil {
		t.Fatalf("re-run in a new session not done: %v %v", done, err)
	}
	apply(t, e)
	if got := read(t, prof); got != first {
		t.Fatalf("re-run rewrote the profile:\n%q\n%q", first, got)
	}
}

func TestHookPwshPathLineWhenOnlySessionHasDir(t *testing.T) {
	prof := filepath.Join(t.TempDir(), "profile.ps1")
	e, _ := hookEnv(t, "windows", nil, map[string]string{"pwsh " + pwshArgs: prof + "\r\n"})
	e.Shell = "pwsh"
	dir := t.TempDir()
	exe := filepath.Join(dir, "oci-assume.exe")
	write(t, exe, "bin")
	winSession(e, exe, true, `C:\Windows\system32`)
	apply(t, e)
	if got := read(t, prof); !strings.Contains(got, "$env:Path = ") {
		t.Fatalf("no PATH line though new shells lack the dir: %q", got)
	}
	winSession(e, exe, false, `C:\Windows\system32`)
	if done, _ := NewHookStep().Done(e); !done {
		t.Fatal("not done in a new session")
	}
}

func TestBinDirScoopShimInNewShellPath(t *testing.T) {
	e, _ := hookEnv(t, "windows", nil, nil)
	root := t.TempDir()
	exe := filepath.Join(root, "scoop", "apps", "oci-assume", "current", "oci-assume.exe")
	shims := filepath.Join(root, "scoop", "shims")
	write(t, exe, "bin")
	write(t, filepath.Join(shims, "oci-assume.exe"), "shim")
	winSession(e, exe, false, `"`+shims+`";C:\x`)
	if _, needed := BinDir(e); needed {
		t.Fatal("scoop shim in the user PATH treated as not on PATH")
	}
}

// On every OS, a PATH line written when needed stays once the session finds the binary.
func TestHookPathLineStableAcrossSessions(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		e, _ := hookEnv(t, goos, nil, nil)
		e.Shell = "zsh"
		dir := filepath.Join(e.Home, ".local", "bin")
		withBinary(t, e, dir, "")
		apply(t, e)
		withBinary(t, e, dir, filepath.Join(dir, "oci-assume"))
		if done, _ := NewHookStep().Done(e); !done {
			t.Fatalf("%s: PATH line flip-flops in a session that finds the binary", goos)
		}
	}
}
