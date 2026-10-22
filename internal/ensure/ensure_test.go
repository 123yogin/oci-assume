package ensure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/token"
)

type fake struct {
	calls      []string
	refreshErr error
	// onRefresh/onAuth run on success to simulate the CLI writing a new token.
	onRefresh func()
	onAuth    func(ctx context.Context) error
}

func (f *fake) Validate(context.Context, string) error {
	f.calls = append(f.calls, "validate")
	return nil
}
func (f *fake) Refresh(context.Context, string) error {
	f.calls = append(f.calls, "refresh")
	if f.refreshErr != nil {
		return f.refreshErr
	}
	if f.onRefresh != nil {
		f.onRefresh()
	}
	return nil
}
func (f *fake) Authenticate(ctx context.Context, _, region string) error {
	f.calls = append(f.calls, "auth:"+region)
	if f.onAuth != nil {
		return f.onAuth(ctx)
	}
	return nil
}

var _ ocicli.Runner = (*fake)(nil)

func writeTok(t *testing.T, path string, remaining time.Duration) {
	t.Helper()
	if err := writeTokErr(path, remaining); err != nil {
		t.Fatal(err)
	}
}

func writeTokErr(path string, remaining time.Duration) error {
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Add(remaining).Unix())))
	body := "aGVhZA." + pl + ".c2ln"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func setup(t *testing.T, remaining time.Duration) (ociconfig.Profile, string) {
	t.Helper()
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	tp := filepath.Join(t.TempDir(), "token")
	if remaining != 0 {
		writeTok(t, tp, remaining)
	}
	return ociconfig.Profile{Name: "dev", Region: "us-phoenix-1", SecurityTokenFile: tp}, tp
}

func TestFreshTokenNoop(t *testing.T) {
	p, _ := setup(t, 55*time.Minute)
	f := &fake{}
	a, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if err != nil || a != ActionNone || len(f.calls) != 0 {
		t.Fatalf("a=%q err=%v calls=%v", a, err, f.calls)
	}
}

func TestNearExpiryRefreshes(t *testing.T) {
	p, tp := setup(t, 5*time.Minute)
	f := &fake{onRefresh: func() { writeTok(t, tp, time.Hour) }}
	a, err := Ensure(context.Background(), f, p, Options{})
	if err != nil || a != ActionRefreshed || len(f.calls) != 1 || f.calls[0] != "refresh" {
		t.Fatalf("a=%q err=%v calls=%v", a, err, f.calls)
	}
}

func TestRefreshFailFallsToLoginInteractive(t *testing.T) {
	p, tp := setup(t, 5*time.Minute)
	f := &fake{refreshErr: ocicli.ErrRefreshFailed, onAuth: func(context.Context) error { writeTok(t, tp, time.Hour); return nil }}
	a, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if err != nil || a != ActionLoggedIn {
		t.Fatalf("a=%q err=%v", a, err)
	}
	if len(f.calls) != 2 || f.calls[0] != "refresh" || f.calls[1] != "auth:us-phoenix-1" {
		t.Fatalf("calls=%v", f.calls)
	}
}

func TestRefreshSuccessButStillExpiredGoesToLogin(t *testing.T) {
	p, tp := setup(t, 5*time.Minute)
	f := &fake{onRefresh: func() { writeTok(t, tp, -time.Minute) }, onAuth: func(context.Context) error { writeTok(t, tp, time.Hour); return nil }}
	a, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if err != nil || a != ActionLoggedIn {
		t.Fatalf("a=%q err=%v calls=%v", a, err, f.calls)
	}
}

func TestExpiredNonInteractive(t *testing.T) {
	p, _ := setup(t, 0)
	f := &fake{}
	_, err := Ensure(context.Background(), f, p, Options{})
	if !errors.Is(err, ocicli.ErrNeedsLogin) {
		t.Fatalf("err=%v", err)
	}
	for _, c := range f.calls {
		if c != "refresh" && c != "validate" {
			t.Fatalf("unexpected call %v", f.calls)
		}
	}
}

// A transient refresh failure on a live session is not a login prompt (I1).
func TestRefreshFailNonInteractiveIsRefreshFailed(t *testing.T) {
	for _, rerr := range []error{errors.New("boom"), fmt.Errorf("%w: 500", ocicli.ErrRefreshFailed)} {
		p, _ := setup(t, 5*time.Minute)
		f := &fake{refreshErr: rerr}
		_, err := Ensure(context.Background(), f, p, Options{})
		if !errors.Is(err, ocicli.ErrRefreshFailed) || errors.Is(err, ocicli.ErrNeedsLogin) {
			t.Fatalf("%v: err=%v", rerr, err)
		}
		if len(f.calls) != 1 {
			t.Fatalf("calls=%v", f.calls)
		}
	}
}

func TestRefreshNeedsLoginNonInteractive(t *testing.T) {
	p, _ := setup(t, 5*time.Minute)
	f := &fake{refreshErr: fmt.Errorf("%w: expired", ocicli.ErrNeedsLogin)}
	if _, err := Ensure(context.Background(), f, p, Options{}); !errors.Is(err, ocicli.ErrNeedsLogin) {
		t.Fatalf("err=%v", err)
	}
}

// C1/R18: a 30-min scheduler with 60-min tokens never lets the session lapse.
func TestBackgroundCadenceNeverExpires(t *testing.T) {
	if got := BackgroundThreshold(10 * time.Minute); got != 45*time.Minute {
		t.Fatalf("BackgroundThreshold(10m)=%v", got)
	}
	if got := BackgroundThreshold(50 * time.Minute); got != 50*time.Minute {
		t.Fatalf("BackgroundThreshold(50m)=%v", got)
	}
	for phase := time.Duration(0); phase < 30*time.Minute; phase += 3 * time.Minute {
		p, tp := setup(t, 0)
		start := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
		now := start
		writeTokAt(t, tp, now.Add(60*time.Minute-phase))
		f := &fake{onRefresh: func() { writeTokAt(t, tp, now.Add(60*time.Minute)) }}
		o := Options{Threshold: BackgroundThreshold(10 * time.Minute), Now: func() time.Time { return now }}
		for ; now.Sub(start) <= 6*time.Hour; now = now.Add(30 * time.Minute) {
			info, err := token.Read(tp, now)
			if err != nil || info.Remaining(now) <= 0 {
				t.Fatalf("phase %v: token expired before run at %v (err=%v)", phase, now.Sub(start), err)
			}
			if _, err := Ensure(context.Background(), f, p, o); err != nil {
				t.Fatalf("phase %v at %v: %v", phase, now.Sub(start), err)
			}
		}
	}
}

func writeTokAt(t *testing.T, path string, exp time.Time) {
	t.Helper()
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	if err := os.WriteFile(path, []byte("aGVhZA."+pl+".c2ln"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInsecureKeyFileRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perms")
	}
	p, _ := setup(t, 5*time.Minute)
	p.KeyFile = filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(p.KeyFile, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.KeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	_, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if !errors.Is(err, token.ErrInsecurePerms) || len(f.calls) != 0 || !strings.Contains(err.Error(), "fix: chmod 600 "+p.KeyFile) {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestLoginTimeoutMessage(t *testing.T) {
	p, _ := setup(t, 0)
	f := &fake{onAuth: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	_, err := Ensure(context.Background(), f, p, Options{Interactive: true, LoginTimeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "browser login not completed within 50ms; increase with --login-timeout") {
		t.Fatalf("err=%v", err)
	}
}

func TestLockBusyMessage(t *testing.T) {
	p, _ := setup(t, 5*time.Minute)
	rel, err := lock.Acquire(context.Background(), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	old := lockWait
	lockWait = 100 * time.Millisecond
	defer func() { lockWait = old }()
	_, err = Ensure(context.Background(), &fake{}, p, Options{})
	want := `another oci-assume is refreshing or logging in to dev; retry in a few seconds`
	if !errors.Is(err, lock.ErrTimeout) || !strings.Contains(err.Error(), want) {
		t.Fatalf("err=%v", err)
	}
}

// M6: Login holds the profile lock and maps a timeout to the M5 message.
func TestLoginTakesLock(t *testing.T) {
	p, tp := setup(t, 0)
	held := false
	f := &fake{onAuth: func(context.Context) error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if rel, err := lock.Acquire(ctx, p.Name); err == nil {
			rel()
		} else {
			held = true
		}
		writeTok(t, tp, time.Hour)
		return nil
	}}
	if err := Login(context.Background(), f, p, "us-ashburn-1", time.Minute); err != nil || !held {
		t.Fatalf("err=%v held=%v calls=%v", err, held, f.calls)
	}
	if len(f.calls) != 1 || f.calls[0] != "auth:us-ashburn-1" {
		t.Fatalf("calls=%v", f.calls)
	}
	rel, err := lock.Acquire(context.Background(), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	old := lockWait
	lockWait = 100 * time.Millisecond
	defer func() { lockWait = old }()
	if err := Login(context.Background(), f, p, "", time.Minute); !errors.Is(err, lock.ErrTimeout) {
		t.Fatalf("busy: err=%v", err)
	}
}

func TestAPIKeyProfileUntouched(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	f := &fake{}
	a, err := Ensure(context.Background(), f, ociconfig.Profile{Name: "key"}, Options{Interactive: true})
	if err != nil || a != ActionNone || len(f.calls) != 0 {
		t.Fatalf("a=%q err=%v", a, err)
	}
	if d, _ := lock.Dir(); d != "" {
		if _, err := os.Stat(d); err == nil {
			t.Error("lock dir created")
		}
	}
}

func TestLoginTimeoutReleasesLock(t *testing.T) {
	p, _ := setup(t, 0)
	f := &fake{onAuth: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	_, err := Ensure(context.Background(), f, p, Options{Interactive: true, LoginTimeout: 100 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel, err := lock.Acquire(ctx, p.Name)
	if err != nil {
		t.Fatalf("lock still held: %v", err)
	}
	rel()
}

func TestThresholdOverride(t *testing.T) {
	p, tp := setup(t, 55*time.Minute)
	f := &fake{onRefresh: func() { writeTok(t, tp, 2*time.Hour) }}
	a, err := Ensure(context.Background(), f, p, Options{Threshold: 60 * time.Minute})
	if err != nil || a != ActionRefreshed {
		t.Fatalf("a=%q err=%v", a, err)
	}
}

func TestInsecurePermsNoRefresh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix perms")
	}
	p, tp := setup(t, 5*time.Minute)
	if err := os.Chmod(tp, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fake{}
	_, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if !errors.Is(err, token.ErrInsecurePerms) || len(f.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

func TestAuthSucceedsButTokenInvalid(t *testing.T) {
	p, _ := setup(t, 0)
	f := &fake{}
	_, err := Ensure(context.Background(), f, p, Options{Interactive: true})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLockTimeoutFreshTokenIsNone(t *testing.T) {
	p, tp := setup(t, 5*time.Minute)
	rel, err := lock.Acquire(context.Background(), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := writeTokErr(tp, time.Hour); err != nil {
			t.Error(err)
		}
	}()
	old := lockWait
	lockWait = 600 * time.Millisecond
	defer func() { lockWait = old }()
	f := &fake{}
	a, err := Ensure(context.Background(), f, p, Options{})
	if err != nil || a != ActionNone || len(f.calls) != 0 {
		t.Fatalf("a=%q err=%v", a, err)
	}
}

func TestLockTimeoutStaleTokenErrors(t *testing.T) {
	p, _ := setup(t, 5*time.Minute)
	rel, err := lock.Acquire(context.Background(), p.Name)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	old := lockWait
	lockWait = 150 * time.Millisecond
	defer func() { lockWait = old }()
	_, err = Ensure(context.Background(), &fake{}, p, Options{})
	if !errors.Is(err, lock.ErrTimeout) {
		t.Fatalf("err=%v", err)
	}
}

func TestCancelDuringRefreshNoLogin(t *testing.T) {
	p, _ := setup(t, 5*time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{}
	r := &cancelRunner{fake: f, cancel: cancel}
	_, err := Ensure(ctx, r, p, Options{Interactive: true})
	if err == nil || len(f.calls) != 1 || f.calls[0] != "refresh" {
		t.Fatalf("err=%v calls=%v", err, f.calls)
	}
}

type cancelRunner struct {
	*fake
	cancel func()
}

func (c *cancelRunner) Refresh(ctx context.Context, p string) error {
	c.calls = append(c.calls, "refresh")
	c.cancel()
	return errors.New("interrupted")
}
