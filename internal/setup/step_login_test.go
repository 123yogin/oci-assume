package setup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/ocicli"
)

type authCall struct{ profile, region string }

// fakeAuth is an ocicli.Runner that records Authenticate calls and returns err.
type fakeAuth struct {
	calls *[]authCall
	err   error
}

func (f fakeAuth) Validate(context.Context, string) error { return nil }
func (f fakeAuth) Refresh(context.Context, string) error  { return nil }
func (f fakeAuth) Authenticate(_ context.Context, p, r string) error {
	*f.calls = append(*f.calls, authCall{p, r})
	return f.err
}

// loginFixture returns a login step on a temp config and a log of Authenticate calls.
func loginFixture(t *testing.T, config string) (*loginStep, *[]authCall) {
	t.Helper()
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir()) // ensure.Login takes the profile lock here
	path := filepath.Join(t.TempDir(), "config")
	if config != "" {
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var calls []authCall
	s := &loginStep{
		configPath: func() string { return path },
		newRunner:  func() (ocicli.Runner, error) { return fakeAuth{calls: &calls}, nil },
	}
	return s, &calls
}

func TestLoginStepDoneWithSessionProfile(t *testing.T) {
	e, _ := testEnv(t, "", true)
	s, _ := loginFixture(t, "[dev]\nsecurity_token_file=/t\n")
	if done, err := s.Done(e); !done || err != nil {
		t.Fatalf("session profile: done %v %v", done, err)
	}
	s, _ = loginFixture(t, "[k]\nkey_file=/k\n")
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("api-key only: done %v %v", done, err)
	}
	s, _ = loginFixture(t, "")
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("missing config: done %v %v", done, err)
	}
	if !s.NeedsTTY() || s.ID() != "first-login" {
		t.Fatal("login step must need a TTY")
	}
	// An unreadable config (here a directory) is not done, and Describe says why.
	dir := t.TempDir()
	s.configPath = func() string { return dir }
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("unreadable config: done %v %v", done, err)
	}
	if d := s.Describe(e); !strings.Contains(d, "could not be read") {
		t.Fatalf("describe %q", d)
	}
}

func TestLoginStepValidatesInput(t *testing.T) {
	e, out := testEnv(t, "bad name\n-x\nok-1\nUS East\nus-east-\neu-frankfurt-1\n", true)
	s, calls := loginFixture(t, "")
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != (authCall{"ok-1", "eu-frankfurt-1"}) {
		t.Fatalf("calls %v", *calls)
	}
	if o := out.String(); strings.Count(o, "invalid profile name") != 2 || strings.Count(o, "invalid region") != 2 {
		t.Fatalf("output %q", o)
	}
	// EOF before a valid answer cancels without authenticating.
	e2, _ := testEnv(t, "bad name\n", true)
	s2, calls2 := loginFixture(t, "")
	if err := s2.Apply(e2, &Receipt{}); err == nil || len(*calls2) != 0 {
		t.Fatalf("err %v calls %v", err, *calls2)
	}
}

func TestLoginStepDefaultNameAndAPIKeyClash(t *testing.T) {
	e, out := testEnv(t, "prod\n\n\n", true)
	s, calls := loginFixture(t, "[DEFAULT]\nregion=us-ashburn-1\n[session]\nkey_file=/k\n[prod]\nkey_file=/k\n")
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	o := out.String()
	if !strings.Contains(o, "profile name [session2]: ") || !strings.Contains(o, `"prod" already exists`) {
		t.Fatalf("output %q", o)
	}
	if len(*calls) != 1 || (*calls)[0].profile != "session2" {
		t.Fatalf("calls %v", *calls)
	}
}

func TestLoginStepCallsAuthenticate(t *testing.T) {
	e, out := testEnv(t, "\n\n", true)
	s, calls := loginFixture(t, "")
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0] != (authCall{"DEFAULT", "us-ashburn-1"}) {
		t.Fatalf("defaults: %v", *calls)
	}
	if o := out.String(); !strings.Contains(o, "profile name [DEFAULT]: ") || !strings.Contains(o, "region [us-ashburn-1]: ") {
		t.Fatalf("prompts %q", o)
	}
	e.In = strings.NewReader("\n\n")
	e.Getenv = func(k string) string {
		if k == "OCI_CLI_REGION" {
			return "ap-tokyo-1"
		}
		return ""
	}
	if err := s.Apply(e, &Receipt{}); err != nil || (*calls)[1] != (authCall{"DEFAULT", "ap-tokyo-1"}) {
		t.Fatalf("OCI_CLI_REGION default: %v %v", err, *calls)
	}
	// A failed login is an error; a missing oci CLI is a skip, before any prompt.
	s.newRunner = func() (ocicli.Runner, error) { return fakeAuth{calls: calls, err: errors.New("boom")}, nil }
	e.In = strings.NewReader("\n\n")
	if err := s.Apply(e, &Receipt{}); err == nil || errors.Is(err, ErrSkipped) {
		t.Fatalf("auth failure: %v", err)
	}
	s.newRunner = func() (ocicli.Runner, error) { return nil, ocicli.ErrNotInstalled }
	if err := s.Apply(e, &Receipt{}); !errors.Is(err, ErrSkipped) {
		t.Fatalf("no oci: %v", err)
	}
	if r := s.SkipReason(e); !strings.Contains(r, "open a new terminal, then run oci-assume setup") {
		t.Fatalf("skip reason %q", r)
	}
	if err := s.Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginFlowCtrlCAtPrompt(t *testing.T) {
	e, _ := testEnv(t, "", true)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	e.In = pr
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	called := false
	_, err := LoginFlow(ctx, e, filepath.Join(t.TempDir(), "config"), func(context.Context, string, string) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("err %v called %v", err, called)
	}
}
