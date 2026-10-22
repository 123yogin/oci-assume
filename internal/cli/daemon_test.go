package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/refresher"
	"github.com/123yogin/oci-assume/internal/state"
)

type fakeBackend struct {
	err         error
	st          refresher.Status
	installed   string
	uninstalled bool
}

func (f *fakeBackend) Install(bin string) error {
	f.installed = bin
	f.st.Installed, f.st.Path = true, "/fake/agent.plist"
	return nil
}
func (f *fakeBackend) Uninstall() error                  { f.uninstalled = true; f.st.Installed = false; return nil }
func (f *fakeBackend) Status() (refresher.Status, error) { return f.st, f.err }

func saveResults(t *testing.T, rs map[string]state.Result) {
	t.Helper()
	if err := state.Save(state.State{LastResult: rs}); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonInstallStatusUninstall(t *testing.T) {
	e := setup(t, "ok")
	if code := e.run("daemon", "install"); code != 0 {
		t.Fatalf("install exit %d: %s", code, e.stderr.String())
	}
	if e.backend.installed != "/opt/oci-assume/bin/oci-assume" {
		t.Fatalf("installed bin %q", e.backend.installed)
	}
	if out := e.stdout.String(); !strings.Contains(out, "/fake/agent.plist") || !strings.Contains(out, "refresh --all --background") {
		t.Fatalf("install out %q", out)
	}
	at := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	saveResults(t, map[string]state.Result{
		"dev":        {At: at, OK: true, Background: true},
		"prod-admin": {At: at.Add(-time.Minute), Msg: "session expired; browser login required", Background: true},
		"manual":     {At: at, OK: true},
	})
	e.backend.st.LastRun, e.backend.st.LastExit = at, 1
	if code := e.run("daemon", "status"); code != 0 {
		t.Fatalf("status exit %d: %s", code, e.stderr.String())
	}
	out := e.stdout.String()
	if strings.Contains(out, " \n") {
		t.Fatalf("trailing spaces:\n%q", out)
	}
	for _, want := range []string{"installed: yes", "/fake/agent.plist", at.Format(time.RFC3339), "dev", "ok", "prod-admin", "failed", "browser login required"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "manual") {
		t.Fatalf("M13: foreground result listed:\n%s", out)
	}
	if code := e.run("daemon", "uninstall"); code != 0 || !e.backend.uninstalled {
		t.Fatalf("uninstall exit %d", code)
	}
	e.backend.st.LastRun = time.Time{}
	e.run("daemon", "status")
	if !strings.Contains(e.stdout.String(), "installed: no") || !strings.Contains(e.stdout.String(), "last refresh: never") {
		t.Fatalf("after uninstall %q", e.stdout.String())
	}
}

func TestDaemonUnsupportedOS(t *testing.T) {
	e := setup(t, "ok")
	e.goos = "plan9"
	e.realScheduler = true
	if code := e.run("daemon", "status"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.stderr.String(), "not supported") {
		t.Fatalf("stderr %q", e.stderr.String())
	}
}

func TestDaemonEvalModeClean(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_ASSUME_EVAL", "1")
	for _, sub := range []string{"status", "install", "uninstall"} {
		e.run("daemon", sub)
		if e.stdout.Len() != 0 || e.stderr.Len() == 0 {
			t.Fatalf("%s: stdout %q", sub, e.stdout.String())
		}
	}
	e.run("doctor")
	if e.stdout.Len() != 0 || e.stderr.Len() == 0 {
		t.Fatalf("doctor: stdout %q", e.stdout.String())
	}
}

// healthy sets up a state where every doctor check passes.
func healthy(t *testing.T) *env {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.backend.st = refresher.Status{Installed: true, Path: "/fake/agent.plist"}
	saveResults(t, map[string]state.Result{"dev": {At: time.Now().Add(-10 * time.Minute), OK: true, Background: true}})
	return e
}

func TestDoctorAllGood(t *testing.T) {
	e := healthy(t)
	if code := e.run("doctor"); code != 0 {
		t.Fatalf("exit %d:\n%s%s", code, e.stdout.String(), e.stderr.String())
	}
	out := e.stdout.String()
	if strings.Contains(out, "✗") || strings.Contains(out, "!") {
		t.Fatalf("unexpected problem:\n%s", out)
	}
	for _, want := range []string{"✓ oci CLI", "oci 3.99.0", "✓ config", "✓ dev:", "✓ shell hook loaded (zsh)", "✓ daemon installed", "✓ last refresh"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(l, "✓ ") {
			t.Fatalf("bad line %q", l)
		}
	}
}

func TestDoctorReportsMissingHook(t *testing.T) {
	e := healthy(t)
	t.Setenv("OCI_ASSUME_SHELL", "")
	t.Setenv("SHELL", "/bin/bash")
	if code := e.run("doctor"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(e.stdout.String(), `✗ shell hook not loaded — fix: eval "$(oci-assume init bash)"`) {
		t.Fatalf("stdout:\n%s", e.stdout.String())
	}
}

func TestDoctorInsecurePerms(t *testing.T) {
	skipWindows(t, "unix modes")
	e := healthy(t)
	if err := os.Chmod(e.tokPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := e.run("doctor"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.stdout.String(), "— fix: chmod 600 "+e.tokPath) {
		t.Fatalf("stdout:\n%s", e.stdout.String())
	}
	e.goos = "windows"
	e.run("doctor")
	if strings.Contains(e.stdout.String(), "chmod") {
		t.Fatalf("perm check ran for windows:\n%s", e.stdout.String())
	}
}

func TestDoctorMissingToken(t *testing.T) {
	e := healthy(t)
	if err := os.Remove(e.tokPath); err != nil {
		t.Fatal(err)
	}
	if code := e.run("doctor"); code != 1 || !strings.Contains(e.stdout.String(), "✗ dev: missing "+e.tokPath+" — fix: oci-assume login dev") {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
}

// A corrupt state file warns on the last-refresh line only; the daemon check still passes.
func TestDoctorStateErrorIsWarning(t *testing.T) {
	e := healthy(t)
	e.backend.err = fmt.Errorf("%w: bad", refresher.ErrLastRun)
	if err := os.WriteFile(filepath.Join(os.Getenv("OCI_ASSUME_STATE_DIR"), "state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := e.run("doctor"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
	out := e.stdout.String()
	if !strings.Contains(out, "✓ daemon installed") || !strings.Contains(out, "! last refresh unknown") {
		t.Fatalf("stdout:\n%s", out)
	}
}

func TestAgoClamps(t *testing.T) {
	if got := ago(-5 * time.Minute); got != "0s" {
		t.Fatalf("got %q", got)
	}
}

func TestDoctorOciMissing(t *testing.T) {
	e := healthy(t)
	e.ociVersion = func(context.Context) (string, error) { return "", fmt.Errorf("x: %w", errors.New("oci CLI not found")) }
	if code := e.run("doctor"); code != 1 || !strings.Contains(e.stdout.String(), "✗ oci CLI") ||
		!strings.Contains(e.stdout.String(), "OCI_ASSUME_OCI_BIN") {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
}

func TestDoctorOciVersionReal(t *testing.T) {
	setup(t, "ok")
	t.Setenv("OCI_ASSUME_OCI_BIN", "/nonexistent/oci")
	if _, err := ociVersion(context.Background()); err == nil {
		t.Fatal("want error for missing override")
	}
	t.Setenv("OCI_ASSUME_OCI_BIN", "")
	t.Setenv("PATH", "")
	if _, err := ociVersion(context.Background()); err == nil {
		t.Fatal("want error with empty PATH")
	}
}

func TestDoctorConfigBroken(t *testing.T) {
	e := healthy(t)
	t.Setenv("OCI_CLI_CONFIG_FILE", "/nonexistent/config")
	if code := e.run("doctor"); code != 1 || !strings.Contains(e.stdout.String(), "✗ config") {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
}

func TestDoctorDaemonAndResultWarnings(t *testing.T) {
	e := healthy(t)
	e.backend.st.Installed = false
	saveResults(t, map[string]state.Result{
		"dev":        {At: time.Now().Add(-3 * time.Hour), OK: true, Background: true},
		"prod-admin": {At: time.Now().Add(-2 * time.Hour), Msg: "boom", Background: true},
	})
	if code := e.run("doctor"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	out := e.stdout.String()
	if !strings.Contains(out, "✗ daemon not installed — fix: oci-assume daemon install") {
		t.Fatalf("daemon line:\n%s", out)
	}
	if !strings.Contains(out, "! last refresh failed for prod-admin") || !strings.Contains(out, "fix: oci-assume login prod-admin") {
		t.Fatalf("result line:\n%s", out)
	}
	// Only the stale-but-OK warning: exit stays 0.
	e.backend.st.Installed = true
	// M13: a fresh manual refresh does not hide a stale background job.
	saveResults(t, map[string]state.Result{
		"dev":    {At: time.Now().Add(-3 * time.Hour), OK: true, Background: true},
		"manual": {At: time.Now(), OK: true},
	})
	if code := e.run("doctor"); code != 0 || !strings.Contains(e.stdout.String(), "! last refresh 3h") {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
	saveResults(t, map[string]state.Result{"manual": {At: time.Now(), OK: true}})
	if code := e.run("doctor"); code != 0 || !strings.Contains(e.stdout.String(), "! no last refresh recorded") {
		t.Fatalf("exit %d:\n%s", code, e.stdout.String())
	}
}

func TestDoctorUnsupportedDaemonWarns(t *testing.T) {
	e := healthy(t)
	e.goos = "plan9"
	e.realScheduler = true
	e.run("doctor")
	if !strings.Contains(e.stdout.String(), "! daemon: not supported on plan9") {
		t.Fatalf("stdout:\n%s", e.stdout.String())
	}
}
