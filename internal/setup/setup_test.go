package setup

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeStep records Apply/Undo calls into a shared log.
type fakeStep struct {
	id       string
	done     bool
	tty      bool
	applyErr error
	log      *[]string
}

func (f *fakeStep) ID() string                    { return f.id }
func (f *fakeStep) Describe(*Env) string          { return "do " + f.id }
func (f *fakeStep) Done(*Env) (bool, error)       { return f.done, nil }
func (f *fakeStep) NeedsTTY() bool                { return f.tty }
func (f *fakeStep) Undo(_ *Env, _ *Receipt) error { *f.log = append(*f.log, "undo "+f.id); return nil }
func (f *fakeStep) Apply(_ *Env, r *Receipt) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	*f.log = append(*f.log, "apply "+f.id)
	r.Files = append(r.Files, FileEntry{Path: "/x/" + f.id, Kind: "block"})
	return nil
}

func testEnv(t *testing.T, in string, interactive bool) (*Env, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	return &Env{
		GOOS: "linux", Home: t.TempDir(), StateDir: t.TempDir(),
		In: strings.NewReader(in), Out: out, Interactive: interactive,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0) },
	}, out
}

func steps(log *[]string, ids ...string) []Step {
	var s []Step
	for _, id := range ids {
		s = append(s, &fakeStep{id: id, log: log})
	}
	return s
}

func TestRunPromptsAndApplies(t *testing.T) {
	e, out := testEnv(t, "y\nn\n", true)
	var log []string
	applied, err := Run(e, steps(&log, "a", "b"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []string{"a"}) || !reflect.DeepEqual(log, []string{"apply a"}) {
		t.Fatalf("applied %v log %v", applied, log)
	}
	o := out.String()
	if strings.Count(o, "[Y/n]") != 2 || !strings.Contains(o, "do a") || !strings.Contains(o, "– b skipped") {
		t.Fatalf("output %q", o)
	}
	if !strings.HasSuffix(o, "Done. Open a new terminal, then run: oci-assume\n") {
		t.Fatalf("final line: %q", o)
	}
	r, err := LoadReceipt(e.StateDir)
	if err != nil || len(r.Files) != 1 || r.Files[0].Path != "/x/a" {
		t.Fatalf("receipt %+v %v", r, err)
	}
}

func TestRunDefaultYesOnEnter(t *testing.T) {
	e, _ := testEnv(t, "\n", true)
	var log []string
	applied, err := Run(e, steps(&log, "a"), Options{})
	if err != nil || !reflect.DeepEqual(applied, []string{"a"}) {
		t.Fatalf("applied %v err %v", applied, err)
	}
}

func TestRunEOFDeclines(t *testing.T) {
	e, _ := testEnv(t, "", true)
	var log []string
	applied, err := Run(e, steps(&log, "a"), Options{})
	if err != nil || len(applied) != 0 {
		t.Fatalf("applied %v err %v", applied, err)
	}
}

func TestRunSkipsDone(t *testing.T) {
	e, out := testEnv(t, "", true)
	var log []string
	s := []Step{&fakeStep{id: "a", done: true, log: &log}, &fakeStep{id: "b", log: &log}}
	applied, err := Run(e, s, Options{Skip: map[string]bool{"b": true}})
	if err != nil || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
	o := out.String()
	if !strings.Contains(o, "✓ a already set up") || !strings.Contains(o, "– b skipped") || strings.Contains(o, "[Y/n]") {
		t.Fatalf("output %q", o)
	}
}

func TestRunDryRunNoApply(t *testing.T) {
	e, out := testEnv(t, "", false)
	var log []string
	applied, err := Run(e, steps(&log, "a"), Options{DryRun: true})
	if err != nil || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
	if o := out.String(); !strings.Contains(o, "do a") || strings.Contains(o, "[Y/n]") {
		t.Fatalf("output %q", o)
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote a receipt: %v", err)
	}
}

func TestRunNonInteractiveNeedsYes(t *testing.T) {
	e, out := testEnv(t, "y\n", false)
	var log []string
	applied, err := Run(e, steps(&log, "a"), Options{})
	if !errors.Is(err, ErrNeedsYes) || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
	if o := out.String(); !strings.Contains(o, "do a") || strings.Contains(o, "Done.") {
		t.Fatalf("plan not printed: %q", o)
	}
}

func TestRunNeedsTTYSkippedNonInteractive(t *testing.T) {
	e, out := testEnv(t, "", false)
	var log []string
	s := []Step{&fakeStep{id: "oci-cli", tty: true, log: &log}, &fakeStep{id: "shell-hook", log: &log}}
	applied, err := Run(e, s, Options{Yes: true})
	if err != nil || !reflect.DeepEqual(applied, []string{"shell-hook"}) {
		t.Fatalf("applied %v err %v", applied, err)
	}
	if o := out.String(); !strings.Contains(o, "– oci-cli needs an interactive terminal; run oci-assume setup") {
		t.Fatalf("output %q", o)
	}
}

func TestRunApplyErrorStops(t *testing.T) {
	e, _ := testEnv(t, "", true)
	var log []string
	s := []Step{&fakeStep{id: "a", log: &log}, &fakeStep{id: "b", applyErr: errors.New("boom"), log: &log}, &fakeStep{id: "c", log: &log}}
	applied, err := Run(e, s, Options{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "b: boom") || !reflect.DeepEqual(applied, []string{"a"}) {
		t.Fatalf("applied %v err %v", applied, err)
	}
	if r, _ := LoadReceipt(e.StateDir); len(r.Files) != 1 {
		t.Fatalf("receipt not saved after a: %+v", r)
	}
}

func TestDefaultStepsOrder(t *testing.T) {
	ids := func(goos string) []string {
		var out []string
		for _, s := range DefaultSteps(&Env{GOOS: goos}) {
			out = append(out, s.ID())
		}
		return out
	}
	if got := ids("darwin"); !reflect.DeepEqual(got, []string{"oci-cli", "shell-hook", "background", "first-login"}) {
		t.Fatalf("darwin %v", got)
	}
	if got := ids("windows"); !reflect.DeepEqual(got, []string{"oci-cli", "exec-policy", "shell-hook", "background", "first-login"}) {
		t.Fatalf("windows %v", got)
	}
}

func TestUninstallReverseOrderAndDeletesReceipt(t *testing.T) {
	e, _ := testEnv(t, "", false)
	if err := SaveReceipt(e.StateDir, Receipt{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	var log []string
	if err := Uninstall(e, steps(&log, "a", "b", "c"), false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(log, []string{"undo c", "undo b", "undo a"}) {
		t.Fatalf("log %v", log)
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt still there: %v", err)
	}
	if _, err := os.Stat(e.StateDir); err != nil {
		t.Fatalf("state dir removed without --purge: %v", err)
	}
}

func TestUninstallTwiceSafe(t *testing.T) {
	e, _ := testEnv(t, "", false)
	var log []string
	for i := 0; i < 2; i++ {
		if err := Uninstall(e, steps(&log, "a"), true); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if _, err := os.Stat(e.StateDir); !os.IsNotExist(err) {
		t.Fatalf("purge kept state dir: %v", err)
	}
}

func TestUninstallPurgeRemovesMacLog(t *testing.T) {
	e, _ := testEnv(t, "", false)
	e.GOOS = "darwin"
	logf := filepath.Join(e.Home, "Library", "Logs", "oci-assume.log")
	if err := os.MkdirAll(filepath.Dir(logf), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logf, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var log []string
	if err := Uninstall(e, steps(&log, "a"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logf); !os.IsNotExist(err) {
		t.Fatalf("log kept: %v", err)
	}
}
