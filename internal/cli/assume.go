package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/ensure"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/picker"
	"github.com/123yogin/oci-assume/internal/regions"
	onboard "github.com/123yogin/oci-assume/internal/setup"
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
	if f.pickRegion && f.region != "" {
		return cfgErrorf("use either -R/--pick-region or -r/--region, not both")
	}
	if f.pickRegion && !d.isTTY() {
		return cfgErrorf("-R/--pick-region needs a terminal; pass -r <region> instead")
	}
	ps, err := loadProfiles()
	name, fresh, picked := "", false, false
	switch {
	case err != nil && (len(args) == 1 || !errors.Is(err, ociconfig.ErrNotFound)):
		return err
	case len(args) == 1:
		name = args[0]
	default:
		if name, fresh, err = pickOrLogin(ctx, d, ps, err, loginTimeout); err != nil {
			return err
		}
		if ps, err = loadProfiles(); err != nil {
			return err
		}
		picked = true
	}
	p, err := findProfile(ps, name)
	if err != nil {
		return err
	}
	// The region step follows the picker, never a named profile (scripts must not block), unless -R.
	// A new profile's login already asked; an invalid config region is left for Validate to reject.
	regionStep := !fresh && (f.pickRegion || (picked && f.region == "" && d.isTTY())) &&
		shell.Validate(shell.Env{Region: p.Region}) == nil
	region := p.Region
	if f.region != "" {
		region = f.region
	} else if regionStep {
		if region, err = chooseRegion(d, ps, p); err != nil {
			return err
		}
	}
	env := shell.Env{Profile: p.Name, Region: region, Session: p.IsSession()}
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
	// Login above used the config region (oci rewrites region= otherwise); the override is export-only.
	p.Region = region
	fmt.Fprintln(d.stderr, humanLine(p, action, d))
	return nil
}

// pickOrLogin offers the first login when there are no profiles at all, else runs the picker.
// loadErr is a missing-config error, kept for when the login is declined or impossible.
// fresh reports a profile just created by the login flow, which already asked for its region.
func pickOrLogin(ctx context.Context, d *deps, ps []ociconfig.Profile, loadErr error, timeout time.Duration) (name string, fresh bool, err error) {
	if d.isTTY() && len(ps) == 0 {
		fmt.Fprint(d.stderr, "oci-assume: no OCI profiles found → create a session profile now? [Y/n] ")
		ok, err := yes(ctx, d.stdin)
		if err != nil {
			return "", false, err
		}
		if ok {
			name, err = newProfile(ctx, d, timeout)
			return name, true, err
		}
	}
	if loadErr != nil {
		return "", false, loadErr
	}
	name, err = choose(d, ps)
	if errors.Is(err, picker.ErrNewProfile) {
		name, err = newProfile(ctx, d, timeout)
		return name, true, err
	}
	return name, false, err
}

// chooseRegion runs the region step; the default row (first, "(none)" when empty) returns p's own region.
func chooseRegion(d *deps, ps []ociconfig.Profile, p ociconfig.Profile) (string, error) {
	items := []picker.RegionItem{{Region: p.Region, Default: true}}
	for _, r := range candidateRegions(ps, p.Region) {
		if r != p.Region {
			items = append(items, picker.RegionItem{Region: r})
		}
	}
	r, err := d.pickRegion(p.Name, items, d.stdin)
	if errors.Is(err, picker.ErrNoTTY) {
		return "", asConfig(err)
	}
	return r, err
}

// candidateRegions is own, then config regions, then $OCI_CLI_REGION, then the built-ins; invalid names are dropped.
func candidateRegions(ps []ociconfig.Profile, own string) []string {
	names := []string{own}
	for _, q := range ps {
		names = append(names, q.Region)
	}
	names = append(names, os.Getenv("OCI_CLI_REGION"))
	valid := names[:0]
	for _, r := range names {
		if shell.Validate(shell.Env{Region: r}) == nil {
			valid = append(valid, r)
		}
	}
	return regions.Merge(valid, regions.Known)
}

// completeRegion completes -r/--region from the same list as the region step; it never runs oci.
func completeRegion(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	ps, _ := ociconfig.Load(ociconfig.DefaultPath())
	var out []string
	for _, r := range candidateRegions(ps, "") {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeProfile completes the profile argument (and -R's) from the config file.
func completeProfile(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ps, _ := ociconfig.Load(ociconfig.DefaultPath())
	var out []string
	for _, p := range ps {
		if strings.HasPrefix(p.Name, prefix) {
			out = append(out, p.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// activeOverride is $OCI_CLI_REGION when it is valid and differs from p's own region, else "".
func activeOverride(p ociconfig.Profile) string {
	r := os.Getenv("OCI_CLI_REGION")
	if r == p.Region || shell.Validate(shell.Env{Region: r}) != nil {
		return ""
	}
	return r
}

// yes reads a [Y/n] answer; Enter is yes, EOF is no, and Ctrl-C returns ctx.Err().
func yes(ctx context.Context, in *os.File) (bool, error) {
	line, err := onboard.ReadLine(ctx, in)
	if cerr := ctx.Err(); cerr != nil {
		return false, cerr
	}
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	}
	return false, nil
}

// newProfile runs the setup login flow with prompts on stderr, so stdout stays eval-safe.
func newProfile(ctx context.Context, d *deps, timeout time.Duration) (string, error) {
	r, err := d.newRunner()
	if err != nil {
		return "", err
	}
	e := &onboard.Env{GOOS: d.goos, In: d.stdin, Out: d.stderr, Interactive: true, Getenv: os.Getenv, Now: d.now}
	return onboard.LoginFlow(ctx, e, ociconfig.DefaultPath(), func(ctx context.Context, name, region string) error {
		return ensure.Login(ctx, r, ociconfig.Profile{Name: name}, region, timeout)
	})
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
	items := make([]picker.Item, len(ps), len(ps)+1)
	for i, p := range ps {
		items[i] = picker.Item{Profile: p, Remaining: remaining(p, now), Active: p.Name == active}
	}
	items = append(items, picker.Item{New: true})
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
