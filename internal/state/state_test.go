package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDirEnv(t *testing.T) {
	d := t.TempDir()
	t.Setenv("OCI_ASSUME_STATE_DIR", d)
	if got, err := Dir(); err != nil || got != d {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, d)
	}
}

func TestNoStateDir(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("cannot simulate")
	}
	t.Setenv("OCI_ASSUME_STATE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := Dir(); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("Dir err = %v", err)
	}
	if _, err := Load(); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("Load err = %v", err)
	}
	if err := Save(State{}); !errors.Is(err, ErrNoStateDir) {
		t.Fatalf("Save err = %v", err)
	}
}

func TestStateMissingIsEmpty(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.LastAssumed) != 0 || len(s.LastResult) != 0 {
		t.Fatalf("expected empty state, got %+v", s)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("OCI_ASSUME_STATE_DIR", filepath.Join(t.TempDir(), "nested"))
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	in := State{
		LastAssumed: map[string]time.Time{"dev": at},
		LastResult:  map[string]Result{"dev": {At: at, OK: false, Msg: "boom", Notified: at.Add(time.Minute)}},
	}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	out, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !out.LastAssumed["dev"].Equal(at) || !reflect.DeepEqual(out.LastResult["dev"].Msg, "boom") ||
		out.LastResult["dev"].OK || !out.LastResult["dev"].Notified.Equal(at.Add(time.Minute)) {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestStateJSONFieldNames(t *testing.T) {
	d := t.TempDir()
	t.Setenv("OCI_ASSUME_STATE_DIR", d)
	if err := Save(State{LastAssumed: map[string]time.Time{"a": time.Now()}, LastResult: map[string]Result{}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"last_assumed"`, `"last_result"`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("missing %s in %s", k, b)
		}
	}
}

func TestStateCorruptErrors(t *testing.T) {
	d := t.TempDir()
	t.Setenv("OCI_ASSUME_STATE_DIR", d)
	p := filepath.Join(d, "state.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	var se *json.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v, want *json.SyntaxError", err)
	}
	if !strings.Contains(err.Error(), p) {
		t.Fatalf("error %q lacks file path", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "{not json" {
		t.Fatal("corrupt file must not be discarded")
	}
}

func TestRecent(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	s := State{LastAssumed: map[string]time.Time{
		"zeta":  now.Add(-1 * time.Hour),
		"alpha": now.Add(-23 * time.Hour),
		"old":   now.Add(-25 * time.Hour),
	}}
	got := s.Recent(now, 24*time.Hour)
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Recent = %v, want %v", got, want)
	}
}

func TestSaveAtomicPerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	d := t.TempDir()
	t.Setenv("OCI_ASSUME_STATE_DIR", d)
	if err := Save(State{}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(d, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(d)
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}
