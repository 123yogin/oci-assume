package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/picker"
	"github.com/123yogin/oci-assume/internal/refresher"
	"github.com/123yogin/oci-assume/internal/shell"
	"github.com/123yogin/oci-assume/internal/state"
	"github.com/123yogin/oci-assume/internal/testutil/fakeoci"
)

type countNotifier struct {
	n      int
	onCall func()
}

func (c *countNotifier) Notify(string, string) error {
	c.n++
	if c.onCall != nil {
		c.onCall()
	}
	return nil
}

type env struct {
	t        *testing.T
	tokPath  string
	logPath  string
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	tty      bool
	notes    countNotifier
	runners  int
	pickName string
	picked   []picker.Item
	goos     string
	// backend replaces the OS scheduler unless realScheduler is set.
	backend       *fakeBackend
	realScheduler bool
	ociVersion    func(context.Context) (string, error)
}

// setup writes a config with a session profile "dev", an API-key profile
// "prod-admin" and a session profile literally named "status".
func setup(t *testing.T, mode string) *env {
	t.Helper()
	bin := fakeoci.Install(t, mode)
	e := &env{t: t, tokPath: os.Getenv("FAKE_TOKEN_PATH"), logPath: os.Getenv("FAKE_OCI_LOG"), backend: &fakeBackend{},
		ociVersion: func(context.Context) (string, error) { return "oci 3.99.0", nil }}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	body := fmt.Sprintf(`[dev]
region=us-phoenix-1
security_token_file=%s
[prod-admin]
region=us-ashburn-1
user=ocid1.user.oc1..x
key_file=%s
[status]
region=us-phoenix-1
security_token_file=%s
`, e.tokPath, filepath.Join(dir, "key.pem"), e.tokPath)
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCI_CLI_CONFIG_FILE", cfg)
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	t.Setenv("OCI_ASSUME_OCI_BIN", bin)
	t.Setenv("OCI_ASSUME_SHELL", "zsh")
	// SHELL picks the hook hint; CI runners default to bash.
	for _, k := range []string{"OCI_CLI_PROFILE", "OCI_ASSUME_EVAL", "OCI_ASSUME_THRESHOLD", "SHELL"} {
		t.Setenv(k, "")
	}
	return e
}

func (e *env) token(remaining time.Duration) {
	e.t.Helper()
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(remaining).Unix())))
	if err := os.WriteFile(e.tokPath, []byte("aGVhZA."+pl+".c2ln"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chmod(e.tokPath, 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) deps() *deps {
	d := defaultDeps()
	d.stdout, d.stderr = &e.stdout, &e.stderr
	d.isTTY = func() bool { return e.tty }
	d.notifier = &e.notes
	if e.goos != "" {
		d.goos = e.goos
	}
	d.pick = func(items []picker.Item, _ *os.File) (string, error) { e.picked = items; return e.pickName, nil }
	if !e.realScheduler {
		d.scheduler = func(string) (refresher.Backend, error) { return e.backend, nil }
	}
	d.binPath = func() (string, error) { return "/opt/oci-assume/bin/oci-assume", nil }
	d.ociVersion = e.ociVersion
	inner := d.newRunner
	d.newRunner = func() (ocicli.Runner, error) { e.runners++; return inner() }
	return d
}

func (e *env) run(args ...string) int {
	e.stdout.Reset()
	e.stderr.Reset()
	return run(context.Background(), e.deps(), args)
}

// eval runs args the way the shell hook does (R19: OCI_ASSUME_EVAL=1).
func (e *env) eval(args ...string) int {
	old := os.Getenv("OCI_ASSUME_EVAL")
	e.t.Setenv("OCI_ASSUME_EVAL", "1")
	defer e.t.Setenv("OCI_ASSUME_EVAL", old)
	return e.run(args...)
}

func (e *env) calls() string {
	b, _ := os.ReadFile(e.logPath)
	return string(b)
}

// skipWindows is for tests that need a POSIX-only fake oci mode.
func skipWindows(t *testing.T, why string) {
	if runtime.GOOS == "windows" {
		t.Skip(why)
	}
}

func TestAssumeEmitsExports(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("dev"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, e.stderr.String())
	}
	want := shell.Export(shell.Zsh, shell.Env{Profile: "dev", Region: "us-phoenix-1", Session: true})
	if e.stdout.String() != want {
		t.Fatalf("stdout %q, want %q", e.stdout.String(), want)
	}
	if !strings.Contains(e.stderr.String(), "dev") {
		t.Fatalf("no human line on stderr: %q", e.stderr.String())
	}
	s, err := state.Load()
	if err != nil || s.LastAssumed["dev"].IsZero() {
		t.Fatalf("LastAssumed not recorded: %v %v", s, err)
	}
}

func TestAssumeEmitsExportsPwsh(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_ASSUME_SHELL", "pwsh")
	if code := e.eval("dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	want := shell.Export(shell.Pwsh, shell.Env{Profile: "dev", Region: "us-phoenix-1", Session: true})
	if e.stdout.String() != want {
		t.Fatalf("stdout %q, want %q", e.stdout.String(), want)
	}
}

func TestAssumeRegionOverride(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("-r", "us-ashburn-1", "dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='us-ashburn-1'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestAssumeAPIKeyNoRunner(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_ASSUME_OCI_BIN", "/nonexistent/oci")
	if code := e.eval("prod-admin"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if strings.Contains(e.stdout.String(), "OCI_CLI_AUTH=") {
		t.Fatalf("api-key profile got auth: %q", e.stdout.String())
	}
}

func TestAssumeUnknownProfile(t *testing.T) {
	e := setup(t, "ok")
	if code := e.eval("nope"); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if e.stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", e.stdout.String())
	}
}

func TestAssumeConfigMissing(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_CLI_CONFIG_FILE", filepath.Join(t.TempDir(), "missing"))
	if code := e.eval("dev"); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
}

func TestAssumeWithoutHook(t *testing.T) {
	for _, v := range []string{"", "tcsh"} {
		e := setup(t, "ok")
		e.goos = "darwin"
		e.token(time.Hour)
		t.Setenv("OCI_ASSUME_SHELL", v)
		if code := e.eval("dev"); code != 1 {
			t.Fatalf("shell %q: exit %d, want 1", v, code)
		}
		if e.stdout.Len() != 0 {
			t.Fatalf("stdout not empty: %q", e.stdout.String())
		}
		if !strings.Contains(e.stderr.String(), `eval "$(oci-assume init zsh)"`) {
			t.Fatalf("no hook hint: %q", e.stderr.String())
		}
	}
}

func TestAssumeWithoutHookWindowsHint(t *testing.T) {
	e := setup(t, "ok")
	e.goos = "windows"
	t.Setenv("OCI_ASSUME_SHELL", "")
	if code := e.eval("dev"); code != 1 || e.stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
	if !strings.Contains(e.stderr.String(), "oci-assume init pwsh | Out-String | Invoke-Expression") ||
		!strings.Contains(e.stderr.String(), "oci-assume --shell pwsh <profile> | Out-String | Invoke-Expression") {
		t.Fatalf("no pwsh hint: %q", e.stderr.String())
	}
}

func TestAssumeCancelledBeforeExport(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_ASSUME_EVAL", "1")
	e.token(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := run(ctx, e.deps(), []string{"dev"}); code != 1 || e.stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
}

func TestStateLockDistinctFromProfiles(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	a, err := lock.Path("__state__")
	if err != nil {
		t.Fatal(err)
	}
	b, err := lock.Path(stateLock)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("profile __state__ shares the state lock %s", a)
	}
}

func TestAssumeNeedsLoginNoTTY(t *testing.T) {
	e := setup(t, "expired")
	e.token(-time.Minute)
	if code := e.eval("dev"); code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, e.stderr.String())
	}
	if e.stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", e.stdout.String())
	}
}

func TestAssumeInteractiveLogin(t *testing.T) {
	e := setup(t, "ok")
	e.token(-time.Minute)
	e.tty = true
	if code := e.eval("dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.calls(), "session authenticate") {
		t.Fatalf("no authenticate: %q", e.calls())
	}
}

func TestAssumeValidateFails(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("-r", "us;rm -rf", "dev"); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if e.stdout.Len() != 0 {
		t.Fatalf("stdout not empty: %q", e.stdout.String())
	}
}

func TestAssumeBadThreshold(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("--threshold", "soon", "dev"); code != 4 {
		t.Fatalf("flag: exit %d, want 4", code)
	}
	t.Setenv("OCI_ASSUME_THRESHOLD", "bad")
	if code := e.eval("dev"); code != 4 {
		t.Fatalf("env: exit %d, want 4", code)
	}
	// The flag overrides a bad env value.
	if code := e.eval("--threshold", "5m", "dev"); code != 0 {
		t.Fatalf("override: exit %d: %s", code, e.stderr.String())
	}
}

func TestThresholdTriggersRefresh(t *testing.T) {
	e := setup(t, "ok")
	e.token(30 * time.Minute)
	t.Setenv("OCI_ASSUME_THRESHOLD", "45m")
	if code := e.eval("dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.calls(), "session refresh") {
		t.Fatalf("no refresh: %q", e.calls())
	}
}

func TestDashDashProfile(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("--", "status"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.HasPrefix(e.stdout.String(), "export OCI_CLI_PROFILE='status'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestNoProfileNoTTY(t *testing.T) {
	e := setup(t, "ok")
	if code := e.eval(); code != 4 {
		t.Fatalf("exit %d, want 4", code)
	}
	if !strings.Contains(e.stderr.String(), "prod-admin") {
		t.Fatalf("profiles not listed: %q", e.stderr.String())
	}
}

func TestNoProfilePicker(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty = true
	e.pickName = "prod-admin"
	if code := e.eval(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stdout.String(), "'prod-admin'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestPickerMarksActiveProfile(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty = true
	e.pickName = "dev"
	t.Setenv("OCI_CLI_PROFILE", "prod-admin")
	if code := e.eval(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	active := map[string]bool{}
	for _, it := range e.picked {
		if !it.New {
			active[it.Profile.Name] = it.Active
		}
	}
	if len(active) != 3 || !active["prod-admin"] || active["dev"] || active["status"] {
		t.Fatalf("active flags %v", active)
	}
}

func TestUnset(t *testing.T) {
	e := setup(t, "ok")
	if code := e.eval("--unset"); code != 0 || e.stdout.String() != shell.Unset(shell.Zsh) {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
	t.Setenv("OCI_ASSUME_SHELL", "")
	if code := e.eval("--unset"); code != 1 || e.stdout.Len() != 0 {
		t.Fatalf("no hook: exit %d stdout %q", code, e.stdout.String())
	}
}

func TestAssumeStateFailureOnlyWarns(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_ASSUME_STATE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	if code := e.eval("prod-admin"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "warning") {
		t.Fatalf("no warning: %q", e.stderr.String())
	}
	if code := e.run("refresh", "--all"); code != 4 {
		t.Fatalf("refresh --all: exit %d, want 4", code)
	}
}

func TestStatusJSON(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("status", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(e.stdout.Bytes(), &rows); err != nil {
		t.Fatalf("%v: %q", err, e.stdout.String())
	}
	if len(rows) != 3 {
		t.Fatalf("rows %v", rows)
	}
	for _, k := range []string{"profile", "region", "session", "expires", "remaining_seconds", "active"} {
		if _, ok := rows[0][k]; !ok {
			t.Fatalf("missing %q in %v", k, rows[0])
		}
	}
	if rows[0]["profile"] != "dev" || rows[0]["active"] != true || rows[0]["session"] != true {
		t.Fatalf("dev row %v", rows[0])
	}
	if rs := rows[0]["remaining_seconds"].(float64); rs < 3500 || rs > 3600 {
		t.Fatalf("remaining %v", rs)
	}
	if rows[1]["profile"] != "prod-admin" || rows[1]["active"] != false || rows[1]["session"] != false {
		t.Fatalf("prod-admin row %v", rows[1])
	}
	if e.runners != 0 {
		t.Fatalf("status built a runner")
	}
}

func TestStatusTable(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour - time.Second)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("status"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := e.stdout.String()
	for _, want := range []string{"PROFILE", "REGION", "EXPIRES", "STATE", "● active", "59m", "api-key", "○ valid"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	e.token(-time.Minute)
	e.run("status")
	out = e.stdout.String()
	if !strings.Contains(out, "○ expired · run: oci-assume dev") || !strings.Contains(out, "○ expired · run: oci-assume -- status") || !strings.Contains(out, "—") {
		t.Fatalf("expired row:\n%s", e.stdout.String())
	}
}

func TestRefreshBackgroundNotifiesOnce(t *testing.T) {
	e := setup(t, "expired")
	e.token(-time.Minute)
	held := false
	e.notes.onCall = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		release, err := lock.Acquire(ctx, stateLock)
		if err != nil {
			held = true
			return
		}
		release()
	}
	for i := 0; i < 2; i++ {
		if code := e.run("refresh", "dev", "--background"); code != 2 {
			t.Fatalf("run %d: exit %d, want 2: %s", i, code, e.stderr.String())
		}
	}
	if e.notes.n != 1 {
		t.Fatalf("notified %d times, want 1", e.notes.n)
	}
	if held {
		t.Fatal("Notify ran while the state lock was held")
	}
	s, _ := state.Load()
	if r := s.LastResult["dev"]; r.OK || r.Notified.IsZero() || r.At.IsZero() {
		t.Fatalf("LastResult %+v", r)
	}
}

func TestRefreshAllRecent(t *testing.T) {
	e := setup(t, "ok")
	e.token(5 * time.Minute)
	if code := e.eval("dev"); code != 0 {
		t.Fatalf("assume: %d %s", code, e.stderr.String())
	}
	e.token(5 * time.Minute)
	if code := e.run("refresh", "--all", "--background"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	s, _ := state.Load()
	if r := s.LastResult["dev"]; !r.OK {
		t.Fatalf("LastResult %+v", r)
	}
	if _, ok := s.LastResult["prod-admin"]; ok {
		t.Fatalf("refreshed a profile not recently assumed")
	}
}

func TestRefreshWorstExit(t *testing.T) {
	e := setup(t, "expired")
	e.token(-time.Minute)
	now := time.Now()
	if err := state.Save(state.State{LastAssumed: map[string]time.Time{"dev": now, "prod-admin": now}}); err != nil {
		t.Fatal(err)
	}
	if code := e.run("refresh", "--all", "--background"); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestPromptNoExec(t *testing.T) {
	e := setup(t, "ok")
	e.token(52*time.Minute + 30*time.Second)
	t.Setenv("OCI_ASSUME_OCI_BIN", "/bin/false")
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("prompt"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.TrimSpace(e.stdout.String()) != "dev 52m" {
		t.Fatalf("stdout %q", e.stdout.String())
	}
	if e.calls() != "" || e.runners != 0 {
		t.Fatalf("prompt touched oci: log %q runners %d", e.calls(), e.runners)
	}
}

func TestPromptEmptyAndUnknown(t *testing.T) {
	e := setup(t, "ok")
	for _, p := range []string{"", "ghost"} {
		t.Setenv("OCI_CLI_PROFILE", p)
		if code := e.run("prompt"); code != 0 || e.stdout.Len() != 0 {
			t.Fatalf("%q: exit %d stdout %q", p, code, e.stdout.String())
		}
	}
	t.Setenv("OCI_CLI_CONFIG_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("prompt"); code != 0 || e.stdout.Len() != 0 {
		t.Fatalf("missing config: exit %d stdout %q", code, e.stdout.String())
	}
}

func TestPromptJSON(t *testing.T) {
	e := setup(t, "ok")
	e.token(5 * time.Minute)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("prompt", "--json"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var m map[string]any
	if err := json.Unmarshal(e.stdout.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["profile"] != "dev" || m["state"] != "warn" || !strings.HasPrefix(m["text"].(string), "dev ") {
		t.Fatalf("json %v", m)
	}
	if rs := m["remaining_seconds"].(float64); rs < 200 || rs > 300 {
		t.Fatalf("remaining %v", rs)
	}
}

func TestPromptLatency(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	d := e.deps()
	const n = 1000
	start := time.Now()
	for i := 0; i < n; i++ {
		run(context.Background(), d, []string{"prompt"})
	}
	// File opens on Windows runners are several times slower (6ms seen), so it gets a looser budget.
	budget := 2 * time.Millisecond
	if runtime.GOOS == "windows" {
		budget = 20 * time.Millisecond
	}
	if avg := time.Since(start) / n; avg > budget {
		t.Fatalf("avg %v per prompt", avg)
	}
}

func TestOciNotInstalled(t *testing.T) {
	e := setup(t, "ok")
	e.token(5 * time.Minute)
	t.Setenv("OCI_ASSUME_OCI_BIN", "/nonexistent/oci")
	if code := e.eval("dev"); code != 3 {
		t.Fatalf("assume: exit %d, want 3: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "https://docs.oracle.com/iaas/Content/API/SDKDocs/cliinstall.htm") {
		t.Fatalf("no install URL: %q", e.stderr.String())
	}
	if code := e.eval("login", "dev"); code != 3 {
		t.Fatalf("login: exit %d, want 3", code)
	}
}

func TestLogin(t *testing.T) {
	e := setup(t, "ok")
	if code := e.run("login", "dev", "-r", "us-ashburn-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.calls(), "session authenticate --profile-name dev --region us-ashburn-1") {
		t.Fatalf("calls %q", e.calls())
	}
	if code := e.run("login", "ghost"); code != 4 {
		t.Fatalf("unknown: exit %d, want 4", code)
	}
}

func TestLockTimeoutExit5(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_ASSUME_EVAL", "1")
	e.token(5 * time.Minute)
	release, err := lock.Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if code := run(ctx, e.deps(), []string{"--threshold", "10m", "dev"}); code != 5 {
		t.Fatalf("exit %d, want 5: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "another oci-assume is refreshing or logging in to dev; retry in a few seconds") {
		t.Fatalf("stderr %q", e.stderr.String())
	}
	// M6: login waits on the same lock.
	if code := e.run("login", "dev"); code != 5 || strings.Contains(e.calls(), "authenticate") {
		t.Fatalf("login: exit %d calls %q", code, e.calls())
	}
}

func TestCtrlCDuringLogin(t *testing.T) {
	skipWindows(t, "needs the POSIX fake's slow mode")
	e := setup(t, "slow")
	t.Setenv("OCI_ASSUME_EVAL", "1")
	e.token(-time.Minute)
	e.tty = true
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	if code := run(ctx, e.deps(), []string{"dev"}); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, e.stderr.String())
	}
	if time.Since(start) > 10*time.Second || e.stdout.Len() != 0 {
		t.Fatalf("slow cancel or stdout %q", e.stdout.String())
	}
	lctx, lcancel := context.WithTimeout(context.Background(), time.Second)
	defer lcancel()
	release, err := lock.Acquire(lctx, "dev")
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	release()
}

func TestInit(t *testing.T) {
	e := setup(t, "ok")
	if code := e.run("init", "zsh"); code != 0 || e.stdout.String() != shell.Init(shell.Zsh) {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
}

func TestVersion(t *testing.T) {
	e := setup(t, "ok")
	if code := e.run("version"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := e.stdout.String(); got != "oci-assume dev (none, unknown)\n" {
		t.Fatalf("stdout %q", got)
	}
}

func TestEvalModeStdoutClean(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_ASSUME_EVAL", "1")
	t.Setenv("OCI_CLI_PROFILE", "dev")
	for _, args := range [][]string{
		{"dev", "--help"}, {"--help"}, {"help"}, {"-r", "us-ashburn-1", "status"}, {"status", "--json"},
		{"version"}, {"--version"}, {"prompt"}, {"nope"}, {"completion", "zsh"}, {"__complete", ""},
		{"init", "zsh"}, {"refresh", "dev"}, {"login", "dev"},
	} {
		e.run(args...)
		if e.stdout.Len() != 0 {
			t.Fatalf("%v: stdout %q", args, e.stdout.String())
		}
		if e.stderr.Len() == 0 {
			t.Fatalf("%v: nothing on stderr", args)
		}
	}
	if code := e.run("dev"); code != 0 || !strings.HasPrefix(e.stdout.String(), "export OCI_CLI_PROFILE='dev'") {
		t.Fatalf("eval assume: exit %d stdout %q", code, e.stdout.String())
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{fmt.Errorf("x: %w", ocicli.ErrNeedsLogin), 2},
		{fmt.Errorf("x: %w: %w", ocicli.ErrNeedsLogin, ocicli.ErrNotInstalled), 2},
		{ocicli.ErrNotInstalled, 3},
		{fmt.Errorf("%w: %w", lock.ErrTimeout, errConfig), 5},
		{state.ErrNoStateDir, 4},
		{picker.ErrCancelled, 1},
		{context.Canceled, 1},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%v: got %d want %d", c.err, got, c.want)
		}
	}
}

func TestPromptIgnoresUnknownFlags(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	if code := e.run("prompt", "--bogus"); code != 0 || !strings.HasPrefix(e.stdout.String(), "dev ") {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
}

// R19: OCI_ASSUME_SHELL alone (it leaks into child shells) no longer permits exports.
func TestAssumeNeedsEvalOrShellFlag(t *testing.T) {
	e := setup(t, "ok")
	e.goos = "darwin"
	e.token(time.Hour)
	t.Setenv("SHELL", "/bin/zsh")
	for _, args := range [][]string{{"dev"}, {"--unset"}} {
		if code := e.run(args...); code != 1 || e.stdout.Len() != 0 {
			t.Fatalf("%v: exit %d stdout %q", args, code, e.stdout.String())
		}
		if !strings.Contains(e.stderr.String(), `eval "$(oci-assume init zsh)"`) || !strings.Contains(e.stderr.String(), "--shell") {
			t.Fatalf("%v: hint %q", args, e.stderr.String())
		}
	}
	if code := e.run("--shell", "bash", "dev"); code != 0 {
		t.Fatalf("--shell bash: exit %d: %s", code, e.stderr.String())
	}
	if want := shell.Export(shell.Bash, shell.Env{Profile: "dev", Region: "us-phoenix-1", Session: true}); e.stdout.String() != want {
		t.Fatalf("stdout %q, want %q", e.stdout.String(), want)
	}
	if code := e.run("--unset", "--shell", "fish"); code != 0 || e.stdout.String() != shell.Unset(shell.Fish) {
		t.Fatalf("unset fish: exit %d stdout %q", code, e.stdout.String())
	}
	if code := e.run("--shell", "tcsh", "dev"); code != 4 || e.stdout.Len() != 0 {
		t.Fatalf("bad --shell: exit %d stdout %q", code, e.stdout.String())
	}
	// Under the wrapper OCI_ASSUME_SHELL picks the dialect, and --shell overrides it.
	t.Setenv("OCI_ASSUME_SHELL", "fish")
	if code := e.eval("dev"); code != 0 || !strings.HasPrefix(e.stdout.String(), "set -gx OCI_CLI_PROFILE") {
		t.Fatalf("eval fish: exit %d stdout %q", code, e.stdout.String())
	}
	if code := e.eval("--shell", "pwsh", "dev"); code != 0 || !strings.HasPrefix(e.stdout.String(), "$env:OCI_CLI_PROFILE") {
		t.Fatalf("eval --shell pwsh: exit %d stdout %q", code, e.stdout.String())
	}
}

// M1: the hint follows $SHELL.
func TestHookHintFollowsSHELL(t *testing.T) {
	e := setup(t, "ok")
	e.goos = "linux"
	t.Setenv("SHELL", "/usr/bin/fish")
	if code := e.run("dev"); code != 1 || !strings.Contains(e.stderr.String(), "oci-assume init fish | source") {
		t.Fatalf("exit %d stderr %q", code, e.stderr.String())
	}
}

// I1: a transient refresh error in the background is exit 1 with no notification.
func TestRefreshBackgroundTransientFailure(t *testing.T) {
	skipWindows(t, "needs the POSIX fake's refresh-error mode")
	e := setup(t, "refresh-error")
	e.token(5 * time.Minute)
	if code := e.run("refresh", "dev", "--background"); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, e.stderr.String())
	}
	if e.notes.n != 0 {
		t.Fatalf("notified %d times", e.notes.n)
	}
	s, _ := state.Load()
	if r := s.LastResult["dev"]; r.OK || !r.Notified.IsZero() || !r.Background || !strings.Contains(r.Msg, "500") {
		t.Fatalf("LastResult %+v", r)
	}
}

// C1: the background run refreshes at 40m left even with the default 10m threshold.
func TestRefreshBackgroundMinThreshold(t *testing.T) {
	e := setup(t, "ok")
	e.token(40 * time.Minute)
	if code := e.run("refresh", "dev"); code != 0 || strings.Contains(e.calls(), "session refresh") {
		t.Fatalf("foreground: exit %d calls %q", code, e.calls())
	}
	if code := e.run("refresh", "dev", "--background"); code != 0 || !strings.Contains(e.calls(), "session refresh") {
		t.Fatalf("background: exit %d calls %q: %s", code, e.calls(), e.stderr.String())
	}
	s, _ := state.Load()
	if !s.LastResult["dev"].Background {
		t.Fatalf("background result not marked: %+v", s.LastResult["dev"])
	}
	e.run("refresh", "dev")
	if s, _ = state.Load(); s.LastResult["dev"].Background {
		t.Fatalf("foreground result marked background")
	}
}

// I3 (Windows): the scheduled task passes captured variables as hidden --env flags.
func TestRefreshEnvFlag(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	dir := t.TempDir()
	t.Setenv("OCI_ASSUME_STATE_DIR", dir)
	if err := state.Save(state.State{LastAssumed: map[string]time.Time{"dev": time.Now()}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	if code := e.run("refresh", "--all", "--background", "--env", "OCI_ASSUME_STATE_DIR="+dir); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	t.Setenv("OCI_ASSUME_STATE_DIR", dir)
	if s, _ := state.Load(); !s.LastResult["dev"].OK {
		t.Fatalf("state not written to --env dir: %+v", s)
	}
	if code := e.run("refresh", "--env", "HOME=/x", "dev"); code != 4 {
		t.Fatalf("disallowed key: exit %d", code)
	}
}

// M3: an insecure token names the fix.
func TestAssumeInsecureTokenFix(t *testing.T) {
	skipWindows(t, "unix modes")
	e := setup(t, "ok")
	e.token(5 * time.Minute)
	if err := os.Chmod(e.tokPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := e.eval("dev"); code != 4 || !strings.Contains(e.stderr.String(), "fix: chmod 600 "+e.tokPath) {
		t.Fatalf("exit %d stderr %q", code, e.stderr.String())
	}
}
