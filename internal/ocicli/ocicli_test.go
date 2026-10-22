package ocicli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/testutil/fakeoci"
	"github.com/123yogin/oci-assume/internal/token"
)

func readLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("FAKE_OCI_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRefreshArgs(t *testing.T) {
	r := NewWithBin(fakeoci.Install(t, "ok"))
	if err := r.Refresh(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readLog(t), "session refresh --profile dev") {
		t.Errorf("log = %q", readLog(t))
	}
	if _, err := os.Stat(os.Getenv("FAKE_TOKEN_PATH")); err != nil {
		t.Errorf("token not written: %v", err)
	}
}

func TestValidateOK(t *testing.T) {
	r := NewWithBin(fakeoci.Install(t, "ok"))
	if err := r.Validate(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readLog(t), "session validate --profile dev") {
		t.Errorf("log = %q", readLog(t))
	}
}

func TestAuthenticateArgs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "auth-ok"))
	if err := r.Authenticate(context.Background(), "dev", "us-phoenix-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readLog(t), "session authenticate --profile-name dev --region us-phoenix-1") {
		t.Errorf("log = %q", readLog(t))
	}
	tok, err := token.Read(os.Getenv("FAKE_TOKEN_PATH"), time.Now())
	if err != nil {
		t.Fatalf("fake token unreadable: %v", err)
	}
	if time.Until(tok.Expiry) < 59*time.Minute {
		t.Errorf("expiry too soon: %v", tok.Expiry)
	}
}

func TestAuthenticateFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "auth-fail"))
	if err := r.Authenticate(context.Background(), "dev", "us-phoenix-1"); err == nil {
		t.Fatal("want error")
	}
}

func TestRefreshFailMapsError(t *testing.T) {
	r := NewWithBin(fakeoci.Install(t, "refresh-fail"))
	err := r.Refresh(context.Background(), "dev")
	if !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("err = %v, want ErrNeedsLogin", err)
	}
}

func TestValidateExpired(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "expired"))
	if err := r.Validate(context.Background(), "dev"); !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("err = %v, want ErrNeedsLogin", err)
	}
}

func TestOtherFailureIncludesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	bin := fakeoci.Install(t, "ok")
	// Unsupported subcommand makes the fake exit 2 with a distinct stderr message.
	err := NewWithBin(bin).(*runner).run(context.Background(), "bogus", "cmd")
	if !errors.Is(err, ErrRefreshFailed) {
		t.Fatalf("err = %v, want ErrRefreshFailed", err)
	}
	if !strings.Contains(err.Error(), "unsupported args") {
		t.Errorf("stderr missing from %q", err)
	}
}

func TestContextTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("slow mode is posix only")
	}
	r := NewWithBin(fakeoci.Install(t, "slow"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Refresh(ctx, "dev")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("child not killed promptly: %v", d)
	}
}

func TestNotInstalled(t *testing.T) {
	bin := fakeoci.Install(t, "ok")
	t.Setenv("PATH", filepath.Dir(bin))
	t.Setenv("OCI_ASSUME_OCI_BIN", "/nonexistent")
	if _, err := New(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled (no PATH fallback)", err)
	}
}

func TestNewResolvesEnvThenPath(t *testing.T) {
	bin := fakeoci.Install(t, "ok")
	t.Setenv("OCI_ASSUME_OCI_BIN", bin)
	t.Setenv("PATH", "")
	if _, err := New(); err != nil {
		t.Fatalf("env: %v", err)
	}
	t.Setenv("OCI_ASSUME_OCI_BIN", "")
	if _, err := New(); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("empty PATH: err = %v, want ErrNotInstalled", err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	if _, err := New(); err != nil {
		t.Fatalf("PATH: %v", err)
	}
}

func capture(t *testing.T, f **os.File) (restore func(), read func() string) {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *f
	*f = pw
	return func() { *f = old }, func() string {
		_ = pw.Close()
		b, _ := io.ReadAll(pr)
		return string(b)
	}
}

func TestAuthenticateKeepsStdoutClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "auth-ok"))
	restoreOut, readOut := capture(t, &os.Stdout)
	restoreErr, readErr := capture(t, &os.Stderr)
	err := r.Authenticate(context.Background(), "dev", "us-phoenix-1")
	restoreOut()
	restoreErr()
	out, errOut := readOut(), readErr()
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("stdout leaked: %q", out)
	}
	if !strings.Contains(errOut, "FAKE-AUTH-STDOUT") {
		t.Errorf("marker did not reach stderr: %q", errOut)
	}
}

// I4: the child must not see the caller's assumed profile, auth or region.
func TestChildEnvStripsOCIVars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "ok"))
	t.Setenv("OCI_CLI_PROFILE", "other")
	t.Setenv("OCI_CLI_AUTH", "security_token")
	t.Setenv("OCI_CLI_REGION", "eu-frankfurt-1")
	t.Setenv("OCI_CLI_CONFIG_FILE", "/keep/config")
	if err := r.Refresh(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if err := r.Authenticate(context.Background(), "dev", "us-phoenix-1"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(os.Getenv("FAKE_OCI_LOG") + ".env")
	if err != nil {
		t.Fatal(err)
	}
	env := string(b)
	for _, k := range []string{"OCI_CLI_PROFILE=", "OCI_CLI_AUTH=", "OCI_CLI_REGION="} {
		if strings.Contains(env, k) {
			t.Errorf("%s leaked to oci: %q", k, env)
		}
	}
	if strings.Count(env, "OCI_CLI_CONFIG_FILE=/keep/config") != 2 {
		t.Errorf("config file not passed through: %q", env)
	}
}

// M7: an empty region is not passed as --region "".
func TestAuthenticateNoRegion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix fake only")
	}
	r := NewWithBin(fakeoci.Install(t, "ok"))
	if err := r.Authenticate(context.Background(), "dev", ""); err != nil {
		t.Fatal(err)
	}
	if l := readLog(t); strings.TrimSpace(l) != "session authenticate --profile-name dev" {
		t.Errorf("log = %q", l)
	}
}

func TestNeedsLoginRealWording(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"refresh at 24h limit", "Your session is no longer valid and cannot be refreshed. Please use 'oci session authenticate' to create a new session.", true},
		{"validate expired", "ERROR: This CLI session has expired, so it cannot currently be used to run commands", true},
		{"mixed case", "Session NO LONGER VALID", true},
		{"unrelated", "connection reset by peer", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := needsLogin(c.msg); got != c.want {
				t.Errorf("needsLogin(%q) = %v, want %v", c.msg, got, c.want)
			}
		})
	}
}
