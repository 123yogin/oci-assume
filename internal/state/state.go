// Package state persists per-profile refresh bookkeeping.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Result records the outcome of the last refresh attempt for a profile.
type Result struct {
	At       time.Time `json:"at"`
	OK       bool      `json:"ok"`
	Msg      string    `json:"msg"`
	Notified time.Time `json:"notified"`
	// Background is set only by `refresh --background`, i.e. the scheduled job.
	Background bool `json:"background,omitempty"`
}

// State is the on-disk bookkeeping, keyed by profile name.
type State struct {
	LastAssumed map[string]time.Time `json:"last_assumed"`
	LastResult  map[string]Result    `json:"last_result"`
}

// ErrNoStateDir means no state directory could be determined.
var ErrNoStateDir = errors.New("cannot determine state directory; set OCI_ASSUME_STATE_DIR")

// Dir returns $OCI_ASSUME_STATE_DIR, else os.UserConfigDir()/oci-assume.
func Dir() (string, error) {
	if d := os.Getenv("OCI_ASSUME_STATE_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return "", ErrNoStateDir
	}
	return filepath.Join(base, "oci-assume"), nil
}

func file() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "state.json"), nil
}

// Load reads the state file; a missing file yields an empty State.
func Load() (State, error) {
	f, err := file()
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(f)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("parse %s: %w", f, err)
	}
	return s, nil
}

// Save writes the state atomically (temp file + rename) with mode 0600.
func Save(s State) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(dir, "state.json")); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// Recent returns the sorted profiles assumed within the given window of now.
func (s State) Recent(now time.Time, within time.Duration) []string {
	out := []string{}
	for p, t := range s.LastAssumed {
		if now.Sub(t) <= within {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
