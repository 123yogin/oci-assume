package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/ensure"
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
)

// loginTimeout matches the default --login-timeout for a browser login.
const loginTimeout = 5 * time.Minute

// Skip reasons when oci is missing: never installed, or installed by setup but not on this PATH yet (e.g. winget).
const (
	notInstalled = "the OCI CLI is not installed; install it, then run oci-assume setup"
	notOnPath    = "the OCI CLI is installed but not on PATH yet; open a new terminal, then run oci-assume setup"
)

// missingReason picks between the two by whether setup recorded installing the OCI CLI.
func missingReason(e *Env) string {
	if r, err := LoadReceipt(e.StateDir); err == nil && r.OCICLI != "" {
		return notOnPath
	}
	return notInstalled
}

var (
	profileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	regionRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
)

type loginStep struct {
	configPath func() string
	newRunner  func() (ocicli.Runner, error)
}

// NewLoginStep returns the first login step.
func NewLoginStep() Step {
	return &loginStep{configPath: ociconfig.DefaultPath, newRunner: ocicli.New}
}

func (*loginStep) ID() string     { return "first-login" }
func (*loginStep) NeedsTTY() bool { return true }

func (s *loginStep) Describe(*Env) string {
	if _, err := HasSessionProfile(s.configPath()); err != nil {
		return "OCI config could not be read (" + err.Error() + ") → create a session profile now?"
	}
	return "no OCI session profiles found → create one now? (asks for a profile name and region, then opens a browser via `oci session authenticate`)"
}

// Done is true once any profile in the OCI config uses a session token; an unreadable config is not done.
func (s *loginStep) Done(*Env) (bool, error) {
	ok, err := HasSessionProfile(s.configPath())
	return ok && err == nil, nil
}

// SkipReason skips without prompting when the oci CLI cannot be found.
func (s *loginStep) SkipReason(e *Env) string {
	if _, err := s.newRunner(); err != nil {
		return missingReason(e)
	}
	return ""
}

// HasSessionProfile reports whether the config at path has a session profile; a missing file is false.
func HasSessionProfile(path string) (bool, error) {
	ps, err := loadConfig(path)
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

// loadConfig reads the OCI config; a missing file is no profiles.
func loadConfig(path string) ([]ociconfig.Profile, error) {
	ps, err := ociconfig.Load(path)
	if errors.Is(err, ociconfig.ErrNotFound) {
		return nil, nil
	}
	return ps, err
}

func (s *loginStep) Apply(e *Env, _ *Receipt) error {
	r, err := s.newRunner()
	if err != nil {
		return fmt.Errorf("%w: %s", ErrSkipped, missingReason(e))
	}
	_, err = LoginFlow(e.Context(), e, s.configPath(), func(ctx context.Context, name, region string) error {
		return ensure.Login(ctx, r, ociconfig.Profile{Name: name}, region, loginTimeout)
	})
	return err
}

// Undo never deletes OCI config or sessions.
func (*loginStep) Undo(*Env, *Receipt) error { return nil }

// LoginFlow asks for a profile name and region on e.In/e.Out, then calls auth; it returns the profile name.
// The default name is DEFAULT if unused, else the first free "session", "session2", ….
func LoginFlow(ctx context.Context, e *Env, configPath string, auth func(ctx context.Context, profile, region string) error) (string, error) {
	ps, err := loadConfig(configPath)
	if err != nil {
		return "", err
	}
	region := "us-ashburn-1"
	if e.Getenv != nil {
		if r := e.Getenv("OCI_CLI_REGION"); regionRe.MatchString(r) {
			region = r
		}
	}
	var name string
	for {
		if name, err = ask(ctx, e, "profile name", defaultName(ps), profileRe, "letters, digits, '.', '_' and '-', at most 64, starting with a letter or digit"); err != nil {
			return "", err
		}
		if p, ok := ociconfig.Find(ps, name); ok && !p.IsSession() {
			fmt.Fprintf(e.Out, "profile %q already exists and is not a session profile; choose another name\n", name)
			continue
		}
		break
	}
	if region, err = ask(ctx, e, "region", region, regionRe, "lowercase letters, digits and '-', e.g. us-ashburn-1"); err != nil {
		return "", err
	}
	fmt.Fprintf(e.Out, "opening a browser for %s (oci session authenticate)…\n", name)
	if err := auth(ctx, name, region); err != nil {
		return "", err
	}
	return name, nil
}

func defaultName(ps []ociconfig.Profile) string {
	if _, ok := ociconfig.Find(ps, "DEFAULT"); !ok {
		return "DEFAULT"
	}
	for i := 1; ; i++ {
		n := "session"
		if i > 1 {
			n += strconv.Itoa(i)
		}
		if _, ok := ociconfig.Find(ps, n); !ok {
			return n
		}
	}
}

// ask prompts until the answer matches re; Enter takes def, EOF and ctx cancel stop.
func ask(ctx context.Context, e *Env, label, def string, re *regexp.Regexp, rule string) (string, error) {
	for {
		fmt.Fprintf(e.Out, "%s [%s]: ", label, def)
		line, err := ReadLine(ctx, e.In)
		if cerr := ctx.Err(); cerr != nil {
			fmt.Fprintln(e.Out)
			return "", cerr
		}
		if err != nil && line == "" {
			fmt.Fprintln(e.Out)
			return "", fmt.Errorf("no %s entered", label)
		}
		v := strings.TrimSpace(line)
		if v == "" {
			v = def
		}
		if re.MatchString(v) {
			return v, nil
		}
		fmt.Fprintf(e.Out, "invalid %s %q (%s)\n", label, v, rule)
	}
}

// ReadLine reads one line byte by byte, so input after it stays unread; ctx cancel returns at once.
// After a cancel the reading goroutine stays blocked until the next input or EOF.
func ReadLine(ctx context.Context, r io.Reader) (string, error) {
	if r == nil {
		return "", io.EOF
	}
	type result struct {
		s   string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 1)
		for {
			n, err := r.Read(buf)
			if n == 1 && buf[0] == '\n' {
				ch <- result{b.String(), nil}
				return
			}
			if n == 1 {
				b.WriteByte(buf[0])
			}
			if err != nil {
				ch <- result{b.String(), err}
				return
			}
		}
	}()
	select {
	case res := <-ch:
		return strings.TrimSuffix(res.s, "\r"), res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
