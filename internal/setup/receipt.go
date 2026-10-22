package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// receiptVersion is the receipt schema version.
const receiptVersion = "1"

const receiptFile = "receipt.json"

// Receipt records what setup changed; it is the source of truth for uninstall and self-heal.
type Receipt struct {
	Version    string           `json:"version"`
	Files      []FileEntry      `json:"files,omitempty"`
	Background *BackgroundEntry `json:"background,omitempty"`
	OCICLI     string           `json:"ociCliInstalledBy,omitempty"`
	LastHeal   time.Time        `json:"lastHeal,omitzero"`
}

// FileEntry is a file setup wrote; Kind is "block" (marked region) or "dropin" (whole file).
type FileEntry struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// BackgroundEntry records the scheduler backend and the user's consent to it.
type BackgroundEntry struct {
	Backend   string `json:"backend"`
	Consented bool   `json:"consented"`
}

// LoadReceipt reads <stateDir>/receipt.json; a missing file yields a zero Receipt.
func LoadReceipt(stateDir string) (Receipt, error) {
	f := filepath.Join(stateDir, receiptFile)
	b, err := os.ReadFile(f) // #nosec G304 -- fixed name under the state dir
	if errors.Is(err, fs.ErrNotExist) {
		return Receipt{}, nil
	}
	if err != nil {
		return Receipt{}, err
	}
	var r Receipt
	if err := json.Unmarshal(b, &r); err != nil {
		return Receipt{}, fmt.Errorf("parse %s: %w", f, err)
	}
	return r, nil
}

// SaveReceipt writes the receipt atomically (temp file + rename) with mode 0600.
func SaveReceipt(stateDir string, r Receipt) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(stateDir, "receipt-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr, os.Chmod(name, 0o600)); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, filepath.Join(stateDir, receiptFile)); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// removeReceipt deletes the receipt; a missing one is fine.
func removeReceipt(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, receiptFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
