package setup

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunHookSkipsUnknownShellWithoutPrompt(t *testing.T) {
	e, out := hookEnv(t, "linux", nil, nil)
	e.Interactive, e.In = true, strings.NewReader("y\n")
	applied, err := Run(e, []Step{NewHookStep()}, Options{})
	if err != nil || len(applied) != 0 {
		t.Fatalf("applied %v err %v", applied, err)
	}
	o := out.String()
	if strings.Contains(o, "[Y/n]") || !strings.Contains(o, "– shell-hook skipped: could not detect your shell") {
		t.Fatalf("output %q", o)
	}
	for _, want := range []string{`eval "$(oci-assume init zsh)"`, "oci-assume init fish | source", "--shell"} {
		if !strings.Contains(o, want) {
			t.Fatalf("missing %q in %q", want, o)
		}
	}
}

func TestRunHookSkipsNoPowerShellWithoutPrompt(t *testing.T) {
	e, out := hookEnv(t, "windows", nil, nil)
	e.Shell, e.Interactive, e.In = "pwsh", true, strings.NewReader("y\n")
	if _, err := Run(e, []Step{NewHookStep()}, Options{}); err != nil {
		t.Fatal(err)
	}
	o := out.String()
	if strings.Contains(o, "[Y/n]") || !strings.Contains(o, "– shell-hook skipped: no PowerShell reported") {
		t.Fatalf("output %q", o)
	}
}

func TestHookApplyNoShellIsErrSkipped(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	err := NewHookStep().Apply(e, &Receipt{})
	if !errors.Is(err, ErrSkipped) || !strings.HasPrefix(err.Error(), "skipped: ") {
		t.Fatalf("err %v", err)
	}
}

func TestRunCtrlCThenEnterDoesNotApply(t *testing.T) {
	e, _ := testEnv(t, "\n\n", true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Ctx = ctx
	var log []string
	applied, err := Run(e, steps(&log, "a", "b"), Options{})
	if !errors.Is(err, context.Canceled) || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
}

func TestRunCancelWhileWaitingStops(t *testing.T) {
	e, _ := testEnv(t, "", true)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	e.In = pr
	ctx, cancel := context.WithCancel(context.Background())
	e.Ctx = ctx
	time.AfterFunc(50*time.Millisecond, cancel)
	var log []string
	applied, err := Run(e, steps(&log, "a", "b"), Options{})
	if !errors.Is(err, context.Canceled) || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
}
