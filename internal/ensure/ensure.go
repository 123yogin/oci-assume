// Package ensure implements the session refresh/login state machine.
package ensure

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/token"
)

// Options tunes Ensure; zero fields take defaults.
type Options struct {
	Interactive  bool
	Threshold    time.Duration // default 10m
	LoginTimeout time.Duration // default 5m
	Now          func() time.Time
}

// Action says what Ensure did.
type Action string

const (
	ActionNone      Action = "none"
	ActionRefreshed Action = "refreshed"
	ActionLoggedIn  Action = "logged-in"
)

var lockWait = 10 * time.Second

// minBackground keeps a 30-min scheduler ahead of 60-min tokens (R18).
const minBackground = 45 * time.Minute

// BackgroundThreshold is the threshold the scheduled refresh uses: max(t, 45m).
func BackgroundThreshold(t time.Duration) time.Duration { return max(t, minBackground) }

// lockBusy is a lock timeout with a message naming the profile.
type lockBusy struct{ profile string }

func (e lockBusy) Error() string {
	return fmt.Sprintf("another oci-assume is refreshing or logging in to %s; retry in a few seconds", e.profile)
}
func (e lockBusy) Unwrap() error { return lock.ErrTimeout }

func acquire(ctx context.Context, profile string) (func(), error) {
	lctx, cancel := context.WithTimeout(ctx, lockWait)
	defer cancel()
	release, err := lock.Acquire(lctx, profile)
	if errors.Is(err, lock.ErrTimeout) && ctx.Err() == nil {
		return nil, lockBusy{profile}
	}
	return release, err
}

// checkKey refuses a key_file readable by group or others (Unix only).
func checkKey(p ociconfig.Profile) error {
	if runtime.GOOS == "windows" || p.KeyFile == "" {
		return nil
	}
	fi, err := os.Stat(p.KeyFile)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return nil
	}
	return fmt.Errorf("profile %q: %w: %s; fix: chmod 600 %s", p.Name, token.ErrInsecurePerms, p.KeyFile, p.KeyFile)
}

// Login runs a forced browser login under the profile lock.
func Login(ctx context.Context, r ocicli.Runner, p ociconfig.Profile, region string, timeout time.Duration) error {
	if err := checkKey(p); err != nil {
		return err
	}
	release, err := acquire(ctx, p.Name)
	if err != nil {
		return err
	}
	defer release()
	return authenticate(ctx, r, p.Name, region, timeout)
}

func authenticate(ctx context.Context, r ocicli.Runner, name, region string, timeout time.Duration) error {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := r.Authenticate(actx, name, region)
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && errors.Is(actx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("profile %q: browser login not completed within %s; increase with --login-timeout: %w", name, timeout, err)
	}
	return fmt.Errorf("profile %q: login: %w", name, err)
}

// Ensure makes the profile's session token usable per the design's state machine.
func Ensure(ctx context.Context, r ocicli.Runner, p ociconfig.Profile, o Options) (Action, error) {
	if !p.IsSession() {
		return ActionNone, nil
	}
	if o.Threshold == 0 {
		o.Threshold = 10 * time.Minute
	}
	if o.LoginTimeout == 0 {
		o.LoginTimeout = 5 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}

	if err := checkKey(p); err != nil {
		return ActionNone, err
	}
	release, err := acquire(ctx, p.Name)
	if err != nil {
		if errors.Is(err, lock.ErrTimeout) {
			// Another process holds the lock; it may have refreshed already.
			rem, rerr := remaining(p, o.Now)
			if rerr != nil {
				return ActionNone, errors.Join(err, rerr)
			}
			if rem > o.Threshold {
				return ActionNone, nil
			}
		}
		return ActionNone, err
	}
	defer release()

	rem, err := remaining(p, o.Now)
	if err != nil {
		return ActionNone, err
	}
	if rem > o.Threshold {
		return ActionNone, nil
	}

	var refreshErr error
	if rem > 0 {
		if refreshErr = r.Refresh(ctx, p.Name); refreshErr == nil {
			if after, err := remaining(p, o.Now); err != nil {
				return ActionNone, err
			} else if after > 0 {
				return ActionRefreshed, nil
			}
		} else if ctx.Err() != nil {
			return ActionNone, fmt.Errorf("profile %q: refresh: %w", p.Name, refreshErr)
		}
	}

	if !o.Interactive {
		// A live session whose refresh failed for another reason is not a login prompt (I1).
		if refreshErr != nil && !errors.Is(refreshErr, ocicli.ErrNeedsLogin) {
			if errors.Is(refreshErr, ocicli.ErrRefreshFailed) {
				return ActionNone, fmt.Errorf("profile %q: refresh: %w", p.Name, refreshErr)
			}
			return ActionNone, fmt.Errorf("profile %q: %w: %w", p.Name, ocicli.ErrRefreshFailed, refreshErr)
		}
		return ActionNone, fmt.Errorf("profile %q: %w; run: oci-assume login %s", p.Name, ocicli.ErrNeedsLogin, p.Name)
	}
	if err := authenticate(ctx, r, p.Name, p.Region, o.LoginTimeout); err != nil {
		return ActionNone, err
	}
	after, err := remaining(p, o.Now)
	if err != nil {
		return ActionNone, err
	}
	if after <= 0 {
		return ActionNone, fmt.Errorf("profile %q: token still invalid after login", p.Name)
	}
	return ActionLoggedIn, nil
}

func remaining(p ociconfig.Profile, now func() time.Time) (time.Duration, error) {
	t := now()
	info, err := token.Read(p.SecurityTokenFile, t)
	if err != nil {
		return 0, fmt.Errorf("profile %q: %w", p.Name, err)
	}
	return info.Remaining(t), nil
}
