package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/refresher"
	onboard "github.com/123yogin/oci-assume/internal/setup"
	"github.com/123yogin/oci-assume/internal/state"
)

// failingBackend is not installed and cannot be installed.
type failingBackend struct{}

func (failingBackend) Install(string) error { return errors.New("no user bus") }
func (failingBackend) Uninstall() error     { return nil }
func (failingBackend) Status() (refresher.Status, error) {
	return refresher.Status{}, nil
}

// tickDeps returns deps whose detached spawns are recorded instead of run.
func tickDeps(e *env, spawned *[][]string, mu *sync.Mutex) *deps {
	d := e.deps()
	d.spawn = func(args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		*spawned = append(*spawned, args)
		return nil
	}
	d.logPath = func() (string, error) { return filepath.Join(e.t.TempDir(), "bg.log"), nil }
	return d
}

func runWith(e *env, d *deps, args ...string) int {
	e.stdout.Reset()
	e.stderr.Reset()
	return run(context.Background(), d, args)
}

func TestTickSpawnsOnceWithin5Min(t *testing.T) {
	e := setup(t, "ok")
	e.token(30*time.Minute + 30*time.Second)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	if code := runWith(e, d, "prompt"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	plain := e.stdout.String()
	for i := 0; i < 3; i++ {
		if code := runWith(e, d, "prompt", "--tick"); code != 0 || e.stdout.String() != plain {
			t.Fatalf("exit %d stdout %q, want %q", code, e.stdout.String(), plain)
		}
	}
	if !reflect.DeepEqual(spawned, [][]string{{"refresh", "dev", "--background"}}) {
		t.Fatalf("spawned %v", spawned)
	}
	dir, _ := state.Dir()
	stamp := filepath.Join(dir, "tick-dev.stamp")
	old := time.Now().Add(-6 * time.Minute)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	runWith(e, d, "prompt", "--tick")
	if len(spawned) != 2 {
		t.Fatalf("stale stamp did not respawn: %v", spawned)
	}
}

func TestTickNoSpawnAbove45m(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_CLI_PROFILE", "dev")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	e.token(50 * time.Minute)
	runWith(e, d, "prompt", "--tick")
	e.token(-time.Minute) // expired: refresh cannot help, login is needed
	runWith(e, d, "prompt", "--tick")
	t.Setenv("OCI_CLI_PROFILE", "prod-admin") // API-key profile
	runWith(e, d, "prompt", "--tick")
	if len(spawned) != 0 {
		t.Fatalf("spawned %v", spawned)
	}
}

func TestTickConcurrentSingleRefresh(t *testing.T) {
	e := setup(t, "ok")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	// A refresh holding the profile lock makes the tick return at once.
	release, err := lock.Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	tick(d, "dev")
	if time.Since(start) > 40*time.Millisecond || len(spawned) != 0 {
		t.Fatalf("busy lock: waited %v spawned %v", time.Since(start), spawned)
	}
	release()
	var wg sync.WaitGroup
	var calls atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); calls.Add(1); tick(d, "dev") }()
	}
	wg.Wait()
	if calls.Load() != 16 || len(spawned) != 1 {
		t.Fatalf("spawned %d times", len(spawned))
	}
}

func TestTickNeverExecsOciInProcess(t *testing.T) {
	e := setup(t, "ok")
	e.token(20 * time.Minute)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	if code := runWith(e, d, "prompt", "--tick"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if e.calls() != "" || e.runners != 0 {
		t.Fatalf("tick touched oci: log %q runners %d", e.calls(), e.runners)
	}
	if len(spawned) != 1 || spawned[0][0] != "refresh" {
		t.Fatalf("spawned %v", spawned)
	}
}

func TestTickLatency(t *testing.T) {
	e := setup(t, "ok")
	e.token(30 * time.Minute)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	var spawned [][]string
	var mu sync.Mutex
	d := tickDeps(e, &spawned, &mu)
	tick(d, "dev") // stamps; later ticks take the fast path
	budget := 2 * time.Millisecond
	if runtime.GOOS == "windows" {
		budget = 20 * time.Millisecond
	}
	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		tick(d, "dev")
	}
	if avg := time.Since(start) / n; avg > budget || len(spawned) != 1 {
		t.Fatalf("avg %v per tick, spawned %d", avg, len(spawned))
	}
}

func TestTickSpawnErrorIsSilent(t *testing.T) {
	e := setup(t, "ok")
	e.token(10 * time.Minute)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	d := e.deps()
	d.spawn = func(...string) error { return errors.New("boom") }
	d.logPath = func() (string, error) { return filepath.Join(t.TempDir(), "bg.log"), nil }
	if code := runWith(e, d, "prompt", "--tick"); code != 0 || e.stderr.Len() != 0 {
		t.Fatalf("exit %d stderr %q", code, e.stderr.String())
	}
}

func TestSelfHealHook(t *testing.T) {
	e := setup(t, "ok")
	dir, _ := state.Dir()
	r := onboard.Receipt{Version: "1", Background: &onboard.BackgroundEntry{Backend: "launchd", Consented: true}}
	if err := onboard.SaveReceipt(dir, r); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(t.TempDir(), "bg.log")
	d := e.deps()
	d.logPath = func() (string, error) { return logf, nil }
	for _, args := range [][]string{{"prompt"}, {"version"}, {"init", "zsh"}} {
		runWith(e, d, args...)
		if e.backend.installed != "" {
			t.Fatalf("%v healed", args)
		}
	}
	if code := runWith(e, d, "status"); code != 0 || e.backend.installed != "/opt/oci-assume/bin/oci-assume" {
		t.Fatalf("exit %d installed %q", code, e.backend.installed)
	}
	got, err := onboard.LoadReceipt(dir)
	if err != nil || got.LastHeal.IsZero() {
		t.Fatalf("LastHeal not saved: %+v %v", got, err)
	}
	// A failing install stays off stderr and goes to the log.
	if err := onboard.SaveReceipt(dir, r); err != nil {
		t.Fatal(err)
	}
	d.scheduler = func(string) (refresher.Backend, error) { return failingBackend{}, nil }
	runWith(e, d, "status")
	if strings.Contains(e.stderr.String(), "no user bus") {
		t.Fatalf("heal error on stderr: %q", e.stderr.String())
	}
	if b, _ := os.ReadFile(logf); !strings.Contains(string(b), "no user bus") {
		t.Fatalf("log %q", b)
	}
}
