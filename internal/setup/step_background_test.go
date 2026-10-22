package setup

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/refresher"
)

// fakeBackend records calls; bin is what InstalledBin reports.
type fakeBackend struct {
	installed            bool
	installs, uninstalls int
	installBin, bin      string
	installErr           error
	statusCalls          int
}

func (f *fakeBackend) Install(bin string) error {
	f.installs++
	f.installBin = bin
	if f.installErr != nil {
		return f.installErr
	}
	f.installed, f.bin = true, bin
	return nil
}

func (f *fakeBackend) Uninstall() error { f.uninstalls++; f.installed = false; return nil }

func (f *fakeBackend) Status() (refresher.Status, error) {
	f.statusCalls++
	return refresher.Status{Installed: f.installed, Path: "/fake/def"}, nil
}

func (f *fakeBackend) InstalledBin() (string, error) { return f.bin, nil }

func bgEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{GOOS: "darwin", Home: t.TempDir(), StateDir: t.TempDir(), Now: time.Now, Out: io.Discard}
}

func testBgStep(b *fakeBackend) *backgroundStep {
	return &backgroundStep{
		backend: func(string) (refresher.Backend, error) { return b, nil },
		bin:     func() (string, error) { return "/opt/bin/oci-assume", nil },
	}
}

func TestBackgroundStepInstallsAndRecordsConsent(t *testing.T) {
	e, b := bgEnv(t), &fakeBackend{}
	s := testBgStep(b)
	if s.ID() != "background" || s.NeedsTTY() {
		t.Fatalf("id %q tty %v", s.ID(), s.NeedsTTY())
	}
	if !strings.Contains(s.Describe(e), "refresh --all --background") {
		t.Fatalf("describe %q", s.Describe(e))
	}
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("done before apply: %v %v", done, err)
	}
	var r Receipt
	if err := s.Apply(e, &r); err != nil {
		t.Fatal(err)
	}
	if b.installs != 1 || b.installBin != "/opt/bin/oci-assume" {
		t.Fatalf("installs %d bin %q", b.installs, b.installBin)
	}
	if r.Background == nil || !r.Background.Consented || r.Background.Backend != "launchd" {
		t.Fatalf("receipt %+v", r.Background)
	}
	if err := SaveReceipt(e.StateDir, r); err != nil {
		t.Fatal(err)
	}
	if done, err := s.Done(e); !done || err != nil {
		t.Fatalf("done after apply: %v %v", done, err)
	}
	// Installed but never consented (e.g. `daemon install`) is not done.
	if err := SaveReceipt(e.StateDir, Receipt{}); err != nil {
		t.Fatal(err)
	}
	if done, _ := s.Done(e); done {
		t.Fatal("done without consent")
	}
}

func TestBackgroundStepUndo(t *testing.T) {
	e, b := bgEnv(t), &fakeBackend{}
	s := testBgStep(b)
	r := Receipt{}
	if err := s.Apply(e, &r); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(e, &r); err != nil {
		t.Fatal(err)
	}
	if b.uninstalls != 1 || b.installed || r.Background != nil {
		t.Fatalf("uninstalls %d installed %v receipt %+v", b.uninstalls, b.installed, r.Background)
	}
	// No receipt (setup never ran) still removes a scheduler entry; twice is safe.
	if err := s.Undo(e, &Receipt{}); err != nil || b.uninstalls != 2 {
		t.Fatalf("second undo: %v %d", err, b.uninstalls)
	}
}

func TestBackgroundStepUnsupportedOS(t *testing.T) {
	e := bgEnv(t)
	s := &backgroundStep{
		backend: func(string) (refresher.Backend, error) { return nil, refresher.ErrUnsupported },
		bin:     func() (string, error) { return "/x", nil },
	}
	if done, err := s.Done(e); !done || err != nil {
		t.Fatalf("unsupported should read as done: %v %v", done, err)
	}
	if err := s.Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
}

func TestSelfHealOnlyWhenConsented(t *testing.T) {
	e := bgEnv(t)
	for _, r := range []Receipt{{}, {Background: &BackgroundEntry{Backend: "launchd", Consented: false}}} {
		b := &fakeBackend{}
		healed, err := SelfHeal(e, &r, b, "/opt/bin/oci-assume")
		if healed || err != nil || b.installs != 0 || !r.LastHeal.IsZero() {
			t.Fatalf("declined/absent consent healed: %v %v installs %d", healed, err, b.installs)
		}
	}
	r := Receipt{Background: &BackgroundEntry{Backend: "launchd", Consented: true}}
	b := &fakeBackend{}
	healed, err := SelfHeal(e, &r, b, "/opt/bin/oci-assume")
	if !healed || err != nil || b.installs != 1 || b.installBin != "/opt/bin/oci-assume" {
		t.Fatalf("consented not healed: %v %v installs %d", healed, err, b.installs)
	}
	// Installed with an existing binary: nothing to do, even after the rate limit.
	e.Now = func() time.Time { return time.Now().Add(time.Hour) }
	b.bin = filepath.Join(t.TempDir())
	if healed, err := SelfHeal(e, &r, b, "/opt/bin/oci-assume"); healed || err != nil || b.installs != 1 {
		t.Fatalf("healthy reinstalled: %v %v %d", healed, err, b.installs)
	}
}

func TestSelfHealWhenBinaryPathMissing(t *testing.T) {
	e := bgEnv(t)
	r := Receipt{Background: &BackgroundEntry{Backend: "launchd", Consented: true}}
	b := &fakeBackend{installed: true, bin: filepath.Join(t.TempDir(), "gone", "oci-assume")}
	healed, err := SelfHeal(e, &r, b, "/opt/bin/oci-assume")
	if !healed || err != nil || b.installs != 1 || b.installBin != "/opt/bin/oci-assume" {
		t.Fatalf("missing binary not healed: %v %v installs %d", healed, err, b.installs)
	}
}

func TestSelfHealRateLimitedOnFailure(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	e := bgEnv(t)
	e.Now = func() time.Time { return now }
	r := Receipt{Background: &BackgroundEntry{Backend: "systemd", Consented: true}}
	b := &fakeBackend{installErr: errors.New("no user bus")}
	if healed, err := SelfHeal(e, &r, b, "/x"); healed || err == nil || b.installs != 1 {
		t.Fatalf("first: %v %v %d", healed, err, b.installs)
	}
	if !r.LastHeal.Equal(now) {
		t.Fatalf("LastHeal not stamped on failure: %v", r.LastHeal)
	}
	now = now.Add(9 * time.Minute)
	if healed, err := SelfHeal(e, &r, b, "/x"); healed || err != nil || b.installs != 1 {
		t.Fatalf("within 10m: %v %v %d", healed, err, b.installs)
	}
	now = now.Add(2 * time.Minute)
	if _, err := SelfHeal(e, &r, b, "/x"); err == nil || b.installs != 2 {
		t.Fatalf("after 10m: %v %d", err, b.installs)
	}
}

func TestSelfHealChecksAtMostEvery10Min(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	e := bgEnv(t)
	e.Now = func() time.Time { return now }
	r := Receipt{Background: &BackgroundEntry{Backend: "launchd", Consented: true}}
	b := &fakeBackend{installed: true, bin: t.TempDir()}
	for i := 0; i < 3; i++ {
		if healed, err := SelfHeal(e, &r, b, "/x"); healed || err != nil {
			t.Fatalf("healthy healed: %v %v", healed, err)
		}
		now = now.Add(3 * time.Minute)
	}
	if b.statusCalls != 1 || !r.LastCheck.Equal(time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)) || !r.LastHeal.IsZero() {
		t.Fatalf("status calls %d LastCheck %v LastHeal %v", b.statusCalls, r.LastCheck, r.LastHeal)
	}
	now = now.Add(2 * time.Minute) // 11m after the first check
	if _, err := SelfHeal(e, &r, b, "/x"); err != nil {
		t.Fatal(err)
	}
	if b.statusCalls != 2 || b.installs != 0 {
		t.Fatalf("status calls %d installs %d", b.statusCalls, b.installs)
	}
}

func TestBackgroundDescribeNamesExistingAgent(t *testing.T) {
	e, b := bgEnv(t), &fakeBackend{}
	s := testBgStep(b)
	if d := s.Describe(e); strings.Contains(d, "replaces") {
		t.Fatalf("fresh install says replaces: %q", d)
	}
	b.installed, b.bin = true, "/usr/local/bin/oci-assume"
	if d := s.Describe(e); !strings.Contains(d, "replaces the existing agent (runs /usr/local/bin/oci-assume)") {
		t.Fatalf("describe %q", d)
	}
}

func TestBackgroundUndoReportsRemoval(t *testing.T) {
	e, b := bgEnv(t), &fakeBackend{installed: true}
	out := &strings.Builder{}
	e.Out = out
	if err := testBgStep(b).Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed the background refresh job") {
		t.Fatalf("output %q", out.String())
	}
	out.Reset()
	if err := testBgStep(b).Undo(e, &Receipt{}); err != nil || out.Len() != 0 {
		t.Fatalf("nothing installed but printed %q (%v)", out.String(), err)
	}
}
