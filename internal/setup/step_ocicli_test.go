package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeTools makes LookPath find only the named tools and records ExecTTY calls.
func fakeTools(e *Env, calls *[]string, fail func(cmd string) error, tools ...string) {
	e.LookPath = func(name string) (string, error) {
		for _, t := range tools {
			if t == name {
				return "/bin/" + name, nil
			}
		}
		return "", errors.New("not found")
	}
	e.ExecTTY = func(_ context.Context, name string, args ...string) error {
		cmd := strings.Join(append([]string{name}, args...), " ")
		*calls = append(*calls, cmd)
		if fail != nil {
			return fail(cmd)
		}
		return nil
	}
}

func TestOCICLIStepBrewCommand(t *testing.T) {
	e, _ := testEnv(t, "", true)
	e.GOOS = "darwin"
	var calls []string
	fakeTools(e, &calls, nil, "brew")
	s := NewOCICLIStep()
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("done %v %v", done, err)
	}
	if !s.NeedsTTY() || !strings.Contains(s.Describe(e), "brew install oci-cli") {
		t.Fatalf("describe %q", s.Describe(e))
	}
	var r Receipt
	if err := s.Apply(e, &r); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"brew install oci-cli"}) || r.OCICLI != "brew" {
		t.Fatalf("calls %v receipt %q", calls, r.OCICLI)
	}
	out := &strings.Builder{}
	e.Out = out
	calls = nil
	if err := s.Undo(e, &r); err != nil || len(calls) != 0 || !strings.Contains(out.String(), "brew uninstall oci-cli") {
		t.Fatalf("undo ran %v or printed %q (%v)", calls, out.String(), err)
	}
}

func TestOCICLIStepDoneWhenFound(t *testing.T) {
	e, _ := testEnv(t, "", true)
	var calls []string
	fakeTools(e, &calls, nil, "oci")
	if done, _ := NewOCICLIStep().Done(e); !done {
		t.Fatal("oci on PATH not detected")
	}
	fakeTools(e, &calls, nil)
	bin := filepath.Join(t.TempDir(), "oci")
	if err := os.WriteFile(bin, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.Getenv = func(k string) string {
		if k == "OCI_ASSUME_OCI_BIN" {
			return bin
		}
		return ""
	}
	if done, _ := NewOCICLIStep().Done(e); !done {
		t.Fatal("OCI_ASSUME_OCI_BIN not honoured")
	}
	// A broken override is not done, even with oci on PATH (matches ocicli.New).
	fakeTools(e, &calls, nil, "oci")
	bin = filepath.Join(t.TempDir(), "missing")
	if done, _ := NewOCICLIStep().Done(e); done {
		t.Fatal("broken OCI_ASSUME_OCI_BIN counted as done")
	}
}

// exitErr fakes an *exec.ExitError's exit code.
type exitErr int

func (c exitErr) Error() string { return "exit status" }
func (c exitErr) ExitCode() int { return int(c) }

func TestOCICLIStepWingetScopeFallback(t *testing.T) {
	e, _ := testEnv(t, "", true)
	e.GOOS = "windows"
	var calls []string
	noInstaller := func(cmd string) error {
		if strings.Contains(cmd, "--scope") {
			return exitErr(int(int32(-1978335216))) // 0x8A150010 as Windows reports it
		}
		return nil
	}
	fakeTools(e, &calls, noInstaller, "winget")
	if d := NewOCICLIStep().Describe(e); !strings.Contains(d, "retries without --scope user") {
		t.Fatalf("describe %q", d)
	}
	var r Receipt
	if err := NewOCICLIStep().Apply(e, &r); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"winget install --id Oracle.OCI-CLI -e --scope user --accept-source-agreements --accept-package-agreements",
		"winget install --id Oracle.OCI-CLI -e --accept-source-agreements --accept-package-agreements",
	}
	if !reflect.DeepEqual(calls, want) || r.OCICLI != "winget" {
		t.Fatalf("calls %v receipt %q", calls, r.OCICLI)
	}
	// Any other failure, or a cancelled run, is not retried.
	calls = nil
	fakeTools(e, &calls, func(string) error { return exitErr(1) }, "winget")
	if err := NewOCICLIStep().Apply(e, &Receipt{}); err == nil || len(calls) != 1 {
		t.Fatalf("generic failure: %v calls %v", err, calls)
	}
	calls = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Ctx = ctx
	fakeTools(e, &calls, noInstaller, "winget")
	if err := NewOCICLIStep().Apply(e, &Receipt{}); err == nil || len(calls) != 1 {
		t.Fatalf("cancelled: %v calls %v", err, calls)
	}
}

func TestOCICLIStepNoInstallerSkips(t *testing.T) {
	e, out := testEnv(t, "", false)
	var calls []string
	fakeTools(e, &calls, nil) // linux, no brew
	var log []string
	s := []Step{NewOCICLIStep(), &fakeStep{id: "shell-hook", log: &log}}
	e.Interactive = true
	e.In = strings.NewReader("y\ny\n")
	applied, err := Run(e, s, Options{})
	if err != nil || !reflect.DeepEqual(applied, []string{"shell-hook"}) || len(calls) != 0 {
		t.Fatalf("applied %v calls %v err %v", applied, calls, err)
	}
	o := out.String()
	if !strings.Contains(o, "– oci-cli skipped: ") || !strings.Contains(o, "oracle/oci-cli") {
		t.Fatalf("output %q", o)
	}
	if r, _ := LoadReceipt(e.StateDir); r.OCICLI != "" {
		t.Fatalf("receipt records %q", r.OCICLI)
	}
	var r Receipt
	if err := NewOCICLIStep().Apply(e, &r); !errors.Is(err, ErrSkipped) {
		t.Fatalf("want ErrSkipped, got %v", err)
	}
}
