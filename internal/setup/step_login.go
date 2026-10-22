package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
)

// loginTimeout matches the default --login-timeout for a browser login.
const loginTimeout = 5 * time.Minute

var (
	profileRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	regionRe  = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// authenticator is the part of ocicli.Runner the login flow needs.
type authenticator interface {
	Authenticate(ctx context.Context, profile, region string) error
}

type loginStep struct {
	configPath func() string
	newRunner  func() (authenticator, error)
}

// NewLoginStep returns the first login step.
func NewLoginStep() Step {
	return &loginStep{
		configPath: ociconfig.DefaultPath,
		newRunner:  func() (authenticator, error) { return ocicli.New() },
	}
}

func (*loginStep) ID() string     { return "first-login" }
func (*loginStep) NeedsTTY() bool { return true }

func (*loginStep) Describe(*Env) string {
	return "no OCI session profiles found → create one now? (asks for a profile name and region, then opens a browser via `oci session authenticate`)"
}

// Done is true once any profile in the OCI config uses a session token.
func (s *loginStep) Done(*Env) (bool, error) {
	return HasSessionProfile(s.configPath())
}

// HasSessionProfile reports whether the config at path has a session profile; a missing file is false.
func HasSessionProfile(path string) (bool, error) {
	ps, err := ociconfig.Load(path)
	if errors.Is(err, ociconfig.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if p.IsSession() {
			return true, nil
		}
	}
	return false, nil
}

func (s *loginStep) Apply(e *Env, _ *Receipt) error {
	r, err := s.newRunner()
	if err != nil {
		return fmt.Errorf("%w: %v; run oci-assume setup again once it is installed", ErrSkipped, err)
	}
	_, err = LoginFlow(context.Background(), e, func(ctx context.Context, p, region string) error {
		ctx, cancel := context.WithTimeout(ctx, loginTimeout)
		defer cancel()
		return r.Authenticate(ctx, p, region)
	})
	return err
}

// Undo never deletes OCI config or sessions.
func (*loginStep) Undo(*Env, *Receipt) error { return nil }

// LoginFlow asks for a profile name and region on e.In/e.Out, then calls auth; it returns the profile name.
func LoginFlow(ctx context.Context, e *Env, auth func(ctx context.Context, profile, region string) error) (string, error) {
	region := "us-ashburn-1"
	if e.Getenv != nil {
		if r := e.Getenv("OCI_CLI_REGION"); regionRe.MatchString(r) {
			region = r
		}
	}
	name, err := ask(e, "profile name", "DEFAULT", profileRe, "letters, digits, '.', '_' and '-' only")
	if err != nil {
		return "", err
	}
	if region, err = ask(e, "region", region, regionRe, "lowercase letters, digits and '-', e.g. us-ashburn-1"); err != nil {
		return "", err
	}
	fmt.Fprintf(e.Out, "opening a browser for %s (oci session authenticate)…\n", name)
	if err := auth(ctx, name, region); err != nil {
		return "", err
	}
	return name, nil
}

// ask prompts until the answer matches re; Enter takes def and EOF cancels.
func ask(e *Env, label, def string, re *regexp.Regexp, rule string) (string, error) {
	for {
		fmt.Fprintf(e.Out, "%s [%s]: ", label, def)
		line, err := readLine(e.In)
		v := strings.TrimSpace(line)
		if err != nil && line == "" {
			fmt.Fprintln(e.Out)
			return "", fmt.Errorf("no %s entered", label)
		}
		if v == "" {
			v = def
		}
		if re.MatchString(v) {
			return v, nil
		}
		fmt.Fprintf(e.Out, "invalid %s %q (%s)\n", label, v, rule)
	}
}

// readLine reads one byte at a time so input after the newline stays unread for the next reader.
func readLine(r io.Reader) (string, error) {
	if r == nil {
		return "", io.EOF
	}
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			return b.String(), err
		}
	}
}
