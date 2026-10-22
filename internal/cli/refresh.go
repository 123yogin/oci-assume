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
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/state"
)

const notifyEvery = 24 * time.Hour

// schedulerEnv are the variables the Windows task may pass with --env.
var schedulerEnv = map[string]bool{"OCI_ASSUME_OCI_BIN": true, "OCI_ASSUME_STATE_DIR": true, "OCI_CLI_CONFIG_FILE": true}

func applyEnv(kvs []string) error {
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !schedulerEnv[k] {
			return cfgErrorf("invalid --env %q", kv)
		}
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}

func newRefresh(d *deps, f *flags) *cobra.Command {
	var all, background bool
	var env []string
	cmd := &cobra.Command{
		Use:   "refresh [profile]",
		Short: "Refresh a session (default $OCI_CLI_PROFILE), or every recently used one with --all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := applyEnv(env); err != nil {
				return err
			}
			threshold, loginTimeout, err := f.durations()
			if err != nil {
				return err
			}
			if background {
				threshold = ensure.BackgroundThreshold(threshold)
			}
			names, err := refreshTargets(d, all, args)
			if err != nil {
				return err
			}
			ps, err := loadProfiles()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			lr := &lazyRunner{newRunner: d.newRunner}
			var errs []error
			for _, name := range names {
				p, err := findProfile(ps, name)
				if err == nil {
					var a ensure.Action
					a, err = ensure.Ensure(ctx, lr, p, ensure.Options{
						Interactive: !background && d.isTTY(), Threshold: threshold,
						LoginTimeout: loginTimeout, Now: d.now,
					})
					err = lr.surface(err)
					if err == nil {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, okText(a))
					}
				}
				if err != nil {
					printErr(d.stderr, fmt.Errorf("%s: %w", name, err))
					errs = append(errs, err)
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if serr := recordResult(ctx, d, name, err, background); serr != nil {
					fmt.Fprintf(d.stderr, "oci-assume: warning: could not record state: %v\n", serr)
				}
			}
			if len(errs) > 0 {
				return reported{errors.Join(errs...)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "refresh every profile assumed in the last 24h")
	cmd.Flags().BoolVar(&background, "background", false, "never open a browser; notify instead")
	cmd.Flags().StringArrayVar(&env, "env", nil, "set KEY=VALUE before running (used by the Windows task)")
	_ = cmd.Flags().MarkHidden("env")
	return cmd
}

func refreshTargets(d *deps, all bool, args []string) ([]string, error) {
	switch {
	case all && len(args) > 0:
		return nil, errors.New("give a profile or --all, not both")
	case all:
		s, err := state.Load()
		if err != nil {
			return nil, err
		}
		return s.Recent(d.now(), 24*time.Hour), nil
	case len(args) == 1:
		return args, nil
	}
	if p := os.Getenv("OCI_CLI_PROFILE"); p != "" {
		return []string{p}, nil
	}
	return nil, cfgErrorf("no profile given and OCI_CLI_PROFILE is unset; run: oci-assume refresh <profile>")
}

func okText(a ensure.Action) string {
	if a == ensure.ActionNone {
		return "ok"
	}
	return string(a)
}

// recordResult writes LastResult; in background mode it notifies a needed login at most once per 24h.
// Notify runs outside the state lock, then Notified is stamped in a second update.
func recordResult(ctx context.Context, d *deps, name string, err error, background bool) error {
	now := d.now()
	notify := false
	if serr := updateState(ctx, func(s *state.State) {
		r := state.Result{At: now, OK: err == nil, Notified: s.LastResult[name].Notified, Background: background}
		if err != nil {
			r.Msg = err.Error()
		}
		notify = background && errors.Is(err, ocicli.ErrNeedsLogin) && now.Sub(r.Notified) >= notifyEvery
		s.LastResult[name] = r
	}); serr != nil || !notify {
		return serr
	}
	body := fmt.Sprintf("%s: session expired; run: oci-assume login %s", name, name)
	if nerr := d.notifier.Notify("oci-assume", body); nerr != nil {
		fmt.Fprintf(d.stderr, "oci-assume: warning: notify: %v\n", nerr)
		return nil
	}
	return updateState(ctx, func(s *state.State) {
		r := s.LastResult[name]
		r.Notified = now
		s.LastResult[name] = r
	})
}
