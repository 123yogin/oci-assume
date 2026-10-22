package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/state"
)

// The prompt tick refreshes below tickBelow remaining, at most once per tickEvery per profile.
const (
	tickBelow = 45 * time.Minute
	tickEvery = 5 * time.Minute
)

// tick starts a detached `refresh <p> --background` unless one ran recently or the profile lock is busy.
// It never waits and never reports errors on the terminal.
func tick(d *deps, profile string) {
	dir, err := state.Dir()
	if err != nil {
		return
	}
	stamp := filepath.Join(dir, "tick-"+sanitize(profile)+".stamp")
	if fresh(d, stamp) {
		return
	}
	lp, err := lock.Path(profile)
	if err != nil || os.MkdirAll(filepath.Dir(lp), 0o700) != nil {
		return
	}
	fl := flock.New(lp)
	if ok, err := fl.TryLock(); !ok || err != nil {
		return
	}
	if fresh(d, stamp) {
		_ = fl.Unlock()
		return
	}
	err = touch(stamp, d.now())
	_ = fl.Unlock()
	if err != nil {
		return
	}
	if err := d.spawn("refresh", profile, "--background"); err != nil {
		logf(d, "tick: start refresh %s: %v", profile, err)
	}
}

func fresh(d *deps, stamp string) bool {
	fi, err := os.Stat(stamp)
	if err != nil {
		return false
	}
	now := d.now()
	return !now.Before(fi.ModTime()) && now.Sub(fi.ModTime()) < tickEvery // a future mtime is stale
}

func touch(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return err
	}
	return os.Chtimes(path, now, now)
}

// sanitize keeps [A-Za-z0-9._-] and maps everything else to '_'.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, s)
}

// defaultLogPath is the launchd log on macOS, else <stateDir>/oci-assume.log.
func defaultLogPath() (string, error) {
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "oci-assume.log"), nil
	}
	dir, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "oci-assume.log"), nil
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- fixed log path
}

// logf appends a timestamped line to the background log; failures are dropped.
func logf(d *deps, format string, a ...any) {
	p, err := d.logPath()
	if err != nil {
		return
	}
	f, err := openLog(p)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%s oci-assume: %s\n", d.now().Format(time.RFC3339), fmt.Sprintf(format, a...))
	_ = f.Close()
}

// spawnDetached starts this binary with args in its own session, output to the log, and does not wait.
func spawnDetached(logPath func() (string, error)) func(args ...string) error {
	return func(args ...string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := exec.Command(exe, args...) // #nosec G204 -- re-runs this binary with fixed args
		if p, err := logPath(); err == nil {
			if f, err := openLog(p); err == nil {
				defer func() { _ = f.Close() }()
				cmd.Stdout, cmd.Stderr = f, f
			}
		}
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
}
