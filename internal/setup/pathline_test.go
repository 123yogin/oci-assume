package setup

import (
	"errors"
	"os"
	"path/filepath"
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
	dir := filepath.Join(t.TempDir(), `a $b"c`)
	withBinary(t, e, dir, "")
	apply(t, e)
	want := `export PATH="` + strings.NewReplacer(`$`, `\$`, `"`, `\"`).Replace(dir) + `:$PATH"`
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
