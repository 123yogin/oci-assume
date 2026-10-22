package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/123yogin/oci-assume/internal/ensure"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/picker"
	"github.com/123yogin/oci-assume/internal/shell"
	"github.com/123yogin/oci-assume/internal/state"
	"github.com/123yogin/oci-assume/internal/token"
)

func runUnset(d *deps, f *flags, args []string) error {
	if len(args) > 0 {
		return errors.New("--unset takes no profile")
	}
	sh, err := hookShell(d, f)
	if err != nil {
		return err
	}
	fmt.Fprint(d.stdout, shell.Unset(sh))
	return nil
}

func runAssume(ctx context.Context, d *deps, f *flags, args []string) error {
	sh, err := hookShell(d, f)
	if err != nil {
		return err
	}
	threshold, loginTimeout, err := f.durations()
	if err != nil {
		return err
	}
	ps, err := loadProfiles()
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	} else if name, err = choose(d, ps); err != nil {
		return err
	}
	p, err := findProfile(ps, name)
	if err != nil {
		return err
	}
	if f.region != "" {
		p.Region = f.region
	}
	env := shell.Env{Profile: p.Name, Region: p.Region, Session: p.IsSession()}
	if err := shell.Validate(env); err != nil {
		return asConfig(err)
	}

	action := ensure.ActionNone
	if p.IsSession() {
		lr := &lazyRunner{newRunner: d.newRunner}
		action, err = ensure.Ensure(ctx, lr, p, ensure.Options{
			Interactive: d.isTTY(), Threshold: threshold, LoginTimeout: loginTimeout, Now: d.now,
		})
		if err != nil {
			return lr.surface(err)
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	now := d.now()
	if err := updateState(ctx, func(s *state.State) { s.LastAssumed[p.Name] = now }); err != nil {
		fmt.Fprintf(d.stderr, "oci-assume: warning: could not record state: %v\n", err)
	}
	fmt.Fprint(d.stdout, shell.Export(sh, env))
	fmt.Fprintln(d.stderr, humanLine(p, action, d))
	return nil
}

// choose runs the picker, or lists profiles and fails without a terminal.
func choose(d *deps, ps []ociconfig.Profile) (string, error) {
	if !d.isTTY() {
		names := make([]string, len(ps))
		for i, p := range ps {
			names[i] = p.Name
		}
		return "", cfgErrorf("no profile given and no terminal for the picker; profiles: %s", strings.Join(names, ", "))
	}
	now := d.now()
	active := os.Getenv("OCI_CLI_PROFILE")
	items := make([]picker.Item, len(ps))
	for i, p := range ps {
		items[i] = picker.Item{Profile: p, Remaining: remaining(p, now), Active: p.Name == active}
	}
	name, err := d.pick(items, d.stdin)
	if errors.Is(err, picker.ErrNoTTY) {
		return "", asConfig(err)
	}
	return name, err
}

func humanLine(p ociconfig.Profile, a ensure.Action, d *deps) string {
	s := "oci-assume: " + p.Name
	if p.Region != "" {
		s += " · " + p.Region
	}
	if !p.IsSession() {
		return s + " · api-key"
	}
	s += " · " + expiresText(remaining(p, d.now()))
	if a != ensure.ActionNone {
		s += " (" + string(a) + ")"
	}
	return s
}

// remaining is the session lifetime left; unreadable tokens count as expired.
func remaining(p ociconfig.Profile, now time.Time) time.Duration {
	if !p.IsSession() {
		return 0
	}
	info, err := token.Read(p.SecurityTokenFile, now)
	if err != nil {
		return 0
	}
	return info.Remaining(now)
}

// expiresText is the prompt's duration text without the profile name.
func expiresText(rem time.Duration) string { return shell.FormatRemaining(rem) }
