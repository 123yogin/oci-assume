// Package lock provides per-profile cross-process file locks.
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/123yogin/oci-assume/internal/state"
	"github.com/gofrs/flock"
)

// ErrTimeout means the lock was not obtained before the context ended.
var ErrTimeout = errors.New("timed out waiting for profile lock")

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Dir returns the lock directory under the state dir.
func Dir() (string, error) {
	d, err := state.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "locks"), nil
}

// Path returns the lock file path: sanitized name (max 100 bytes), short sha256, ".lock".
func Path(profile string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return pathIn(dir, profile), nil
}

func pathIn(dir, profile string) string {
	sum := sha256.Sum256([]byte(profile))
	clean := unsafeChars.ReplaceAllString(profile, "_")
	if len(clean) > 100 {
		clean = clean[:100]
	}
	return filepath.Join(dir, clean+"-"+hex.EncodeToString(sum[:])[:8]+".lock")
}

// Acquire locks the profile, polling every 50ms until ctx ends (ErrTimeout).
// The returned release is safe to call more than once.
func Acquire(ctx context.Context, profile string) (func(), error) {
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return AcquireIn(ctx, dir, profile)
}

// AcquireIn is Acquire with the lock file in dir instead of the default lock directory.
func AcquireIn(ctx context.Context, dir, name string) (func(), error) {
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	fl := flock.New(pathIn(dir, name))
	ok, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if !ok {
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = fl.Unlock() }) }, nil
}
