// Package ocicli runs the oci CLI's session subcommands.
package ocicli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/procattr"
)

var (
	ErrNotInstalled  = errors.New("oci CLI not found")
	ErrNeedsLogin    = errors.New("session expired; browser login required")
	ErrRefreshFailed = errors.New("oci session command failed")
)

// Runner invokes oci session subcommands.
type Runner interface {
	Validate(ctx context.Context, profile string) error
	Refresh(ctx context.Context, profile string) error
	// Authenticate inherits stdio for browser prompts.
	Authenticate(ctx context.Context, profile, region string) error
}

type runner struct{ bin string }

// New resolves $OCI_ASSUME_OCI_BIN, then PATH; an unusable override never falls back.
func New() (Runner, error) {
	if p := os.Getenv("OCI_ASSUME_OCI_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil { //nolint:gosec // G703: user-set oci binary path
			return nil, fmt.Errorf("%w: %s", ErrNotInstalled, p)
		}
		return NewWithBin(p), nil
	}
	p, err := exec.LookPath("oci")
	if err != nil {
		return nil, ErrNotInstalled
	}
	return NewWithBin(p), nil
}

func NewWithBin(path string) Runner { return &runner{bin: path} }

func (r *runner) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.bin, args...) //nolint:gosec // G204: runs the user-configured oci binary
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = childEnv(os.Environ())
	return cmd
}

// childEnv drops the assumed profile's variables so --profile alone selects the session.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "OCI_CLI_PROFILE", "OCI_CLI_AUTH", "OCI_CLI_REGION":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (r *runner) Validate(ctx context.Context, profile string) error {
	return r.run(ctx, "session", "validate", "--profile", profile)
}

func (r *runner) Refresh(ctx context.Context, profile string) error {
	return r.run(ctx, "session", "refresh", "--profile", profile)
}

// run captures stderr and maps failures to sentinel errors.
func (r *runner) run(ctx context.Context, args ...string) error {
	cmd := r.command(ctx, args...)
	procattr.Hide(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	msg := strings.TrimSpace(stderr.String())
	if needsLogin(msg) {
		return fmt.Errorf("%w: %s", ErrNeedsLogin, msg)
	}
	return fmt.Errorf("%w: %v: %s", ErrRefreshFailed, err, msg)
}

// needsLogin matches case-insensitively; the 24h refresh limit uses "no longer valid".
func needsLogin(msg string) bool {
	low := strings.ToLower(msg)
	for _, p := range []string{"not valid", "expired", "no longer valid", "cannot be refreshed"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func (r *runner) Authenticate(ctx context.Context, profile, region string) error {
	args := []string{"session", "authenticate", "--profile-name", profile}
	if region != "" {
		args = append(args, "--region", region)
	}
	cmd := r.command(ctx, args...)
	procattr.HideIfNoConsole(cmd) // keep the terminal when interactive
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr // keep our stdout clean
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: authenticate: %v", ErrRefreshFailed, err)
	}
	return nil
}
