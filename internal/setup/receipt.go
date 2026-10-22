package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
)

// receiptVersion is the receipt schema version.
const receiptVersion = "1"

const receiptFile = "receipt.json"

// errCorruptReceipt marks a receipt that exists but cannot be parsed.
var errCorruptReceipt = errors.New("corrupt receipt")

// Receipt records what setup changed; it is the source of truth for uninstall and self-heal.
type Receipt struct {
	Version    string           `json:"version"`
	Files      []FileEntry      `json:"files,omitempty"`
	Background *BackgroundEntry `json:"background,omitempty"`
	OCICLI     string           `json:"ociCliInstalledBy,omitempty"`
	LastHeal   time.Time        `json:"lastHeal,omitzero"`
	LastCheck  time.Time        `json:"lastCheck,omitzero"` // last self-heal status check
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
		return Receipt{}, fmt.Errorf("%w %s: %w", errCorruptReceipt, f, err)
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

// removeReceipt deletes the receipt and reports whether one existed.
func removeReceipt(stateDir string) (bool, error) {
	err := os.Remove(filepath.Join(stateDir, receiptFile))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// receiptLock names the receipt's lock, like the state lock; no shell-safe profile name can equal it.
const receiptLock = "\x00receipt"

// receiptLockWait bounds waiting for another process's receipt write; a var so tests can shorten it.
var receiptLockWait = 5 * time.Second

// LockReceipt takes the cross-process receipt lock in <stateDir>/locks; it returns lock.ErrTimeout when busy.
func LockReceipt(ctx context.Context, stateDir string) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, receiptLockWait)
	defer cancel()
	return lock.AcquireIn(ctx, filepath.Join(stateDir, "locks"), receiptLock)
}

// UpdateReceipt runs a receipt read-modify-write under the receipt lock; fn returns whether to save.
func UpdateReceipt(ctx context.Context, stateDir string, fn func(*Receipt) bool) error {
	_, err := updateReceipt(ctx, stateDir, fn)
	return err
}

func updateReceipt(ctx context.Context, stateDir string, fn func(*Receipt) bool) (Receipt, error) {
	release, err := LockReceipt(ctx, stateDir)
	if err != nil {
		return Receipt{}, fmt.Errorf("lock receipt: %w", err)
	}
	defer release()
	r, err := LoadReceipt(stateDir)
	if err != nil {
		return r, err
	}
	if !fn(&r) {
		return r, nil
	}
	return r, SaveReceipt(stateDir, r)
}

// mergeStep copies what one Apply changed (before → after) onto cur, keeping other writers' fields.
func mergeStep(cur *Receipt, before, after Receipt) {
	for _, f := range after.Files {
		if !hasFileIn(before.Files, f.Path) && !hasFileIn(cur.Files, f.Path) {
			cur.Files = append(cur.Files, f)
		}
	}
	if !reflect.DeepEqual(before.Background, after.Background) {
		cur.Background = after.Background
	}
	if before.OCICLI != after.OCICLI {
		cur.OCICLI = after.OCICLI
	}
	cur.Version = receiptVersion
}
