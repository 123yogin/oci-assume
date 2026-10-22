package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	onboard "github.com/123yogin/oci-assume/internal/setup"
	"github.com/123yogin/oci-assume/internal/state"
)

func saveConsent(t *testing.T, consented bool) string {
	t.Helper()
	dir, _ := state.Dir()
	r := onboard.Receipt{Version: "1", Background: &onboard.BackgroundEntry{Backend: "launchd", Consented: consented}}
	if err := onboard.SaveReceipt(dir, r); err != nil {
		t.Fatal(err)
	}
	return dir
}

func healDeps(e *env) *deps {
	d := e.deps()
	d.logPath = func() (string, error) { return filepath.Join(e.t.TempDir(), "bg.log"), nil }
	return d
}

func TestDaemonUninstallStopsSelfHeal(t *testing.T) {
	e := setup(t, "ok")
	dir, _ := state.Dir()
	d := healDeps(e)
	if code := runWith(e, d, "daemon", "install"); code != 0 {
		t.Fatalf("install exit %d", code)
	}
	if r, _ := onboard.LoadReceipt(dir); r.Background == nil || !r.Background.Consented || r.Background.Backend != onboard.BackendName(d.goos) {
		t.Fatalf("install did not record consent: %+v", r.Background)
	}
	if code := runWith(e, d, "daemon", "uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d", code)
	}
	if r, _ := onboard.LoadReceipt(dir); r.Background == nil || r.Background.Consented {
		t.Fatalf("uninstall did not revoke consent: %+v", r.Background)
	}
	e.backend.installed = ""
	runWith(e, d, "status")
	if e.backend.installed != "" {
		t.Fatal("self-heal reinstalled after daemon uninstall")
	}
}

func TestDaemonStatusDoesNotHeal(t *testing.T) {
	e := setup(t, "ok")
	saveConsent(t, true)
	if code := runWith(e, healDeps(e), "daemon", "status"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if e.backend.installed != "" || !strings.Contains(e.stdout.String(), "installed: no") {
		t.Fatalf("healed before reporting: %q %q", e.backend.installed, e.stdout.String())
	}
}

func TestSelfHealSkipsBackgroundRefresh(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	saveConsent(t, true)
	runWith(e, healDeps(e), "refresh", "dev", "--background")
	if e.backend.installed != "" {
		t.Fatal("refresh --background child healed")
	}
	runWith(e, healDeps(e), "refresh", "dev")
	if e.backend.installed == "" {
		t.Fatal("foreground refresh did not heal")
	}
}

func TestTickFutureStampIsStale(t *testing.T) {
	e := setup(t, "ok")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	dir, _ := state.Dir()
	stamp := filepath.Join(dir, "tick-dev.stamp")
	if err := touch(stamp, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	tick(d, "dev")
	if len(spawned) != 1 {
		t.Fatalf("future stamp blocked the tick: %v", spawned)
	}
	if fi, _ := os.Stat(stamp); fi.ModTime().After(time.Now().Add(time.Minute)) {
		t.Fatal("stamp not reset")
	}
}

func TestSelfHealSkipsSilentlyWhileReceiptLocked(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	dir := saveConsent(t, true)
	release, err := onboard.LockReceipt(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	d := healDeps(e)
	logFile := filepath.Join(t.TempDir(), "bg.log")
	d.logPath = func() (string, error) { return logFile, nil }
	runWith(e, d, "refresh", "dev")
	if e.backend.installed != "" {
		t.Fatal("healed while another process held the receipt lock")
	}
	if fileHas(logFile, "self-heal") {
		t.Fatal("lock timeout was logged")
	}
}

func fileHas(p, s string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), s)
}
