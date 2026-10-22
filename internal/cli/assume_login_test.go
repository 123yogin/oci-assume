package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/picker"
	onboard "github.com/123yogin/oci-assume/internal/setup"
)

// loginRunner fakes oci: Authenticate appends a session profile to the config and writes a token.
type loginRunner struct {
	e      *env
	cfg    string
	authed []string
}

func (l *loginRunner) Validate(context.Context, string) error { return nil }
func (l *loginRunner) Refresh(context.Context, string) error  { return nil }
func (l *loginRunner) Authenticate(_ context.Context, p, r string) error {
	l.authed = append(l.authed, p+" "+r)
	if err := os.MkdirAll(filepath.Dir(l.cfg), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.cfg, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString("[" + p + "]\nregion=" + r + "\nsecurity_token_file=" + l.e.tokPath + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	l.e.token(time.Hour)
	return err
}

// stdinFile returns an *os.File that reads s.
func stdinFile(t *testing.T, s string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// runLogin evals oci-assume with args, typed input and the fake login runner.
func runLogin(t *testing.T, e *env, cfg, input string, pick func([]picker.Item, *os.File) (string, error), args ...string) (int, *loginRunner) {
	t.Helper()
	t.Setenv("OCI_ASSUME_EVAL", "1")
	t.Setenv("OCI_CLI_CONFIG_FILE", cfg)
	lr := &loginRunner{e: e, cfg: cfg}
	d := e.deps()
	d.stdin = stdinFile(t, input)
	d.newRunner = func() (ocicli.Runner, error) { return lr, nil }
	if pick != nil {
		d.pick = pick
	}
	e.stdout.Reset()
	e.stderr.Reset()
	return run(t.Context(), d, args), lr
}

func TestAssumeZeroProfilesOffersLogin(t *testing.T) {
	e := setup(t, "ok")
	e.tty = true
	cfg := filepath.Join(t.TempDir(), "missing", "config")
	code, lr := runLogin(t, e, cfg, "y\nmyprof\nus-phoenix-1\n", nil)
	if code != 0 || len(lr.authed) != 1 || lr.authed[0] != "myprof us-phoenix-1" {
		t.Fatalf("exit %d authed %v stderr %q", code, lr.authed, e.stderr.String())
	}
	if !strings.HasPrefix(e.stdout.String(), "export OCI_CLI_PROFILE='myprof'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
	if se := e.stderr.String(); !strings.Contains(se, "no OCI profiles found") || !strings.Contains(se, "profile name [DEFAULT]") {
		t.Fatalf("stderr %q", se)
	}

	// Declining keeps the old error; no terminal never prompts.
	cfg = filepath.Join(t.TempDir(), "config")
	if code, lr = runLogin(t, e, cfg, "n\n", nil); code != 4 || len(lr.authed) != 0 {
		t.Fatalf("decline: exit %d authed %v", code, lr.authed)
	}
	e.tty = false
	if code, lr = runLogin(t, e, cfg, "y\n\n\n", nil); code != 4 || len(lr.authed) != 0 || strings.Contains(e.stderr.String(), "create a session profile now") {
		t.Fatalf("no tty: exit %d authed %v stderr %q", code, lr.authed, e.stderr.String())
	}
}

func TestAssumeAPIKeyOnlyGetsPickerWithoutPrompt(t *testing.T) {
	e := setup(t, "ok")
	e.tty = true
	cfg := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(cfg, []byte("[k]\nregion=us-ashburn-1\nkey_file=/k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var picked []picker.Item
	pick := func(items []picker.Item, _ *os.File) (string, error) { picked = items; return "k", nil }
	code, lr := runLogin(t, e, cfg, "", pick)
	if code != 0 || len(lr.authed) != 0 || len(picked) != 2 || !picked[1].New {
		t.Fatalf("exit %d authed %v picked %v stderr %q", code, lr.authed, picked, e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "[Y/n]") {
		t.Fatalf("prompted: %q", e.stderr.String())
	}
}

func TestPickerNewProfileRunsLogin(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty = true
	cfg := os.Getenv("OCI_CLI_CONFIG_FILE")
	pick := func([]picker.Item, *os.File) (string, error) { return "", picker.ErrNewProfile }
	code, lr := runLogin(t, e, cfg, "newp\n\n", pick)
	if code != 0 || len(lr.authed) != 1 || lr.authed[0] != "newp us-ashburn-1" {
		t.Fatalf("exit %d authed %v stderr %q", code, lr.authed, e.stderr.String())
	}
	if !strings.HasPrefix(e.stdout.String(), "export OCI_CLI_PROFILE='newp'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestSetupEnvSharesOneBufferedReader(t *testing.T) {
	e := setup(t, "ok")
	d := e.deps()
	var in io.Reader
	d.setupSteps = func(env *onboard.Env) []onboard.Step { in = env.In; return nil }
	if code := run(t.Context(), d, []string{"setup", "--dry-run"}); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if _, ok := in.(*bufio.Reader); !ok {
		t.Fatalf("Env.In is %T, want *bufio.Reader", in)
	}
}
