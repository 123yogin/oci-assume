package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/lock"
)

// concurrentStep writes the on-disk receipt during Apply, like a self-heal in another process.
type concurrentStep struct{ fakeStep }

func (c *concurrentStep) Apply(e *Env, r *Receipt) error {
	disk, _ := LoadReceipt(e.StateDir)
	disk.LastCheck = time.Unix(1_700_000_000, 0).UTC()
	if err := SaveReceipt(e.StateDir, disk); err != nil {
		return err
	}
	r.OCICLI = "brew"
	return c.fakeStep.Apply(e, r)
}

func TestRunSaveKeepsConcurrentReceiptWrites(t *testing.T) {
	e, _ := testEnv(t, "", true)
	var log []string
	s := &concurrentStep{fakeStep{id: "a", log: &log}}
	if _, err := Run(e, []Step{s}, Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if r.LastCheck.IsZero() || r.OCICLI != "brew" || len(r.Files) != 1 {
		t.Fatalf("receipt %+v", r)
	}
}

func TestReceiptWritersWaitForLock(t *testing.T) {
	e, _ := testEnv(t, "", true)
	defer func(d time.Duration) { receiptLockWait = d }(receiptLockWait)
	receiptLockWait = 100 * time.Millisecond
	release, err := LockReceipt(e.Context(), e.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var log []string
	if _, err := Run(e, steps(&log, "a"), Options{Yes: true}); err == nil || !strings.Contains(err.Error(), "receipt") {
		t.Fatalf("run saved while locked: %v", err)
	}
	if err := Uninstall(e, steps(&log, "a"), false); err == nil {
		t.Fatal("uninstall ran while locked")
	}
	if err := UpdateReceipt(e.Context(), e.StateDir, func(*Receipt) bool { return true }); err == nil {
		t.Fatal("update ran while locked")
	}
	if _, err := LockReceipt(e.Context(), e.StateDir); err != lock.ErrTimeout {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

func TestUninstallReportsReceiptAndState(t *testing.T) {
	e, out := testEnv(t, "", true)
	e.StateDir = filepath.Join(t.TempDir(), "state")
	if err := SaveReceipt(e.StateDir, Receipt{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	var log []string
	if err := Uninstall(e, steps(&log, "a"), true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Removed the setup receipt " + filepath.Join(e.StateDir, "receipt.json") + "\n",
		"Removed the state directory " + e.StateDir + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
	if _, err := os.Stat(e.StateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir kept: %v", err)
	}
}
