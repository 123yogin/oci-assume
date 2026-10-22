package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	onboard "github.com/123yogin/oci-assume/internal/setup"
)

type cliStep struct {
	id  string
	tty bool
	log *[]string
}

func (s *cliStep) ID() string                      { return s.id }
func (s *cliStep) Describe(*onboard.Env) string    { return "plan " + s.id }
func (s *cliStep) Done(*onboard.Env) (bool, error) { return false, nil }
func (s *cliStep) NeedsTTY() bool                  { return s.tty }
func (s *cliStep) Apply(_ *onboard.Env, _ *onboard.Receipt) error {
	*s.log = append(*s.log, "apply "+s.id)
	return nil
}
func (s *cliStep) Undo(_ *onboard.Env, _ *onboard.Receipt) error {
	*s.log = append(*s.log, "undo "+s.id)
	return nil
}

// runSetup runs args with fake setup steps and returns the step log.
func runSetup(e *env, eval bool, args ...string) (int, []string) {
	var log []string
	d := e.deps()
	d.setupSteps = func(*onboard.Env) []onboard.Step {
		return []onboard.Step{&cliStep{id: "oci-cli", tty: true, log: &log}, &cliStep{id: "shell-hook", log: &log}, &cliStep{id: "background", log: &log}}
	}
	if eval {
		e.t.Setenv("OCI_ASSUME_EVAL", "1")
	}
	e.stdout.Reset()
	e.stderr.Reset()
	code := run(e.t.Context(), d, args)
	return code, log
}

func TestSetupYesAppliesAndPrintsToStdout(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("CI", "")
	e.tty = true
	code, log := runSetup(e, false, "setup", "--yes")
	if code != 0 || !reflect.DeepEqual(log, []string{"apply oci-cli", "apply shell-hook", "apply background"}) {
		t.Fatalf("exit %d log %v stderr %q", code, log, e.stderr.String())
	}
	if !strings.Contains(e.stdout.String(), "Done. Open a new terminal, then run: oci-assume") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestSetupStdoutCleanUnderEval(t *testing.T) {
	e := setup(t, "ok")
	e.tty = true
	t.Setenv("CI", "")
	code, _ := runSetup(e, true, "setup", "--yes")
	if code != 0 || e.stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
	if !strings.Contains(e.stderr.String(), "Done.") {
		t.Fatalf("stderr %q", e.stderr.String())
	}
}

func TestSetupNonInteractiveExit2(t *testing.T) {
	e := setup(t, "ok")
	code, log := runSetup(e, false, "setup")
	if code != 2 || len(log) != 0 {
		t.Fatalf("exit %d log %v", code, log)
	}
	if !strings.Contains(e.stderr.String(), "--yes") || !strings.Contains(e.stdout.String(), "plan shell-hook") {
		t.Fatalf("stdout %q stderr %q", e.stdout.String(), e.stderr.String())
	}
}

func TestSetupCIIsNonInteractive(t *testing.T) {
	e := setup(t, "ok")
	e.tty = true
	t.Setenv("CI", "true")
	code, log := runSetup(e, false, "setup", "--yes")
	if code != 0 || !reflect.DeepEqual(log, []string{"apply shell-hook", "apply background"}) {
		t.Fatalf("exit %d log %v", code, log)
	}
}

func TestSetupNoFlagsFillSkip(t *testing.T) {
	e := setup(t, "ok")
	e.tty = true
	t.Setenv("CI", "")
	code, log := runSetup(e, false, "setup", "--yes", "--no-oci-cli", "--no-shell", "--no-background", "--no-login")
	if code != 0 || len(log) != 0 {
		t.Fatalf("exit %d log %v", code, log)
	}
	for _, id := range []string{"oci-cli", "shell-hook", "background"} {
		if !strings.Contains(e.stdout.String(), "– "+id+" skipped") {
			t.Fatalf("%s not skipped: %q", id, e.stdout.String())
		}
	}
}

func TestSetupDryRun(t *testing.T) {
	e := setup(t, "ok")
	code, log := runSetup(e, false, "setup", "--dry-run")
	if code != 0 || len(log) != 0 || !strings.Contains(e.stdout.String(), "plan background") {
		t.Fatalf("exit %d log %v stdout %q", code, log, e.stdout.String())
	}
}

func TestSetupBadShell(t *testing.T) {
	e := setup(t, "ok")
	if code, _ := runSetup(e, false, "setup", "--shell", "tcsh", "--dry-run"); code != 4 {
		t.Fatalf("exit %d", code)
	}
}

func TestUninstallCommand(t *testing.T) {
	e := setup(t, "ok")
	dir := os.Getenv("OCI_ASSUME_STATE_DIR")
	if err := onboard.SaveReceipt(dir, onboard.Receipt{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	code, log := runSetup(e, false, "uninstall")
	if code != 0 || !reflect.DeepEqual(log, []string{"undo background", "undo shell-hook", "undo oci-cli"}) {
		t.Fatalf("exit %d log %v stderr %q", code, log, e.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt kept: %v", err)
	}
	if !strings.Contains(e.stdout.String(), "brew uninstall") {
		t.Fatalf("no removal hints: %q", e.stdout.String())
	}
	if code, _ := runSetup(e, false, "uninstall", "--purge"); code != 0 {
		t.Fatalf("second uninstall exit %d", code)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("purge kept state dir: %v", err)
	}
}

func TestSetupEnvCarriesCommandContext(t *testing.T) {
	e := setup(t, "ok")
	var got context.Context
	d := e.deps()
	d.setupSteps = func(se *onboard.Env) []onboard.Step { got = se.Context(); return nil }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run(ctx, d, []string{"setup", "--dry-run"})
	if got == nil || got.Err() == nil {
		t.Fatalf("Env.Ctx is not the command context: %v", got)
	}
}
