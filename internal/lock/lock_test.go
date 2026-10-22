package lock

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/state"
)

func TestLockPathSanitizes(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	p, err := Path("a/b c$d")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(p)
	if strings.ContainsAny(base, "/ $") {
		t.Fatalf("unsanitized base %q", base)
	}
	if !strings.HasSuffix(base, ".lock") {
		t.Fatalf("missing suffix: %q", base)
	}
	q, _ := Path("a_b_c_d")
	if base == filepath.Base(q) {
		t.Fatal("distinct names must not collide")
	}
	if d, _ := Dir(); filepath.Dir(p) != d {
		t.Fatalf("path %q not under Dir %q", p, d)
	}
}

func TestLockPathLongName(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	a, _ := Path(strings.Repeat("x", 300))
	b, _ := Path(strings.Repeat("x", 301))
	if n := len(filepath.Base(a)); n > 100+len("-12345678.lock") {
		t.Fatalf("base too long: %d", n)
	}
	if a == b {
		t.Fatal("long names differing past truncation must not collide")
	}
	rel, err := Acquire(context.Background(), strings.Repeat("x", 300))
	if err != nil {
		t.Fatal(err)
	}
	rel()
}

func TestNoStateDir(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("cannot simulate")
	}
	t.Setenv("OCI_ASSUME_STATE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := Path("x"); !errors.Is(err, state.ErrNoStateDir) {
		t.Fatalf("Path err = %v", err)
	}
	if _, err := Acquire(context.Background(), "x"); !errors.Is(err, state.ErrNoStateDir) {
		t.Fatalf("Acquire err = %v", err)
	}
}

func TestAcquireExclusive(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	rel, err := Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, "dev"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	rel()
	rel2, err := Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	rel2()
}

func TestAcquireCancelledCtx(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Acquire(ctx, "dev"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
}

func TestReleaseTwiceSafe(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	rel, err := Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	rel()
	rel()
	rel2, err := Acquire(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	rel()
	// A stale second release must not drop rel2's lock.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, "dev"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("lock was dropped by stale release: %v", err)
	}
	rel2()
}

func TestDifferentProfilesIndependent(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	r1, err := Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	r2, err := Acquire(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	r2()
}
