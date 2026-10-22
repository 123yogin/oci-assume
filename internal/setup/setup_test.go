package setup

import (
	"bytes"
	"context"
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
	undoErr  error
	log      *[]string
}

func (f *fakeStep) ID() string              { return f.id }
func (f *fakeStep) Describe(*Env) string    { return "do " + f.id }
func (f *fakeStep) Done(*Env) (bool, error) { return f.done, nil }
func (f *fakeStep) NeedsTTY() bool          { return f.tty }
func (f *fakeStep) Undo(_ *Env, _ *Receipt) error {
	*f.log = append(*f.log, "undo "+f.id)
	return f.undoErr
}

// Apply records its file before failing, like a partial apply.
func (f *fakeStep) Apply(_ *Env, r *Receipt) error {
	r.Files = append(r.Files, FileEntry{Path: "/x/" + f.id, Kind: "block"})
	if f.applyErr != nil {
		return f.applyErr
	}
	*f.log = append(*f.log, "apply "+f.id)
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
	applied, err := Run(e, steps(&log, "shell-hook"), Options{})
	if !errors.Is(err, ErrNeedsYes) || len(applied) != 0 || len(log) != 0 {
		t.Fatalf("applied %v log %v err %v", applied, log, err)
	}
	if o := out.String(); !strings.Contains(o, "do shell-hook") || strings.Contains(o, "Done.") {
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

func TestRunApplyErrorSavesPartialReceipt(t *testing.T) {
	e, _ := testEnv(t, "", true)
	var log []string
	s := []Step{&fakeStep{id: "a", log: &log}, &fakeStep{id: "b", applyErr: errors.New("boom"), log: &log}, &fakeStep{id: "c", log: &log}}
	applied, err := Run(e, s, Options{Yes: true})
	if err == nil || !strings.Contains(err.Error(), "b: boom") || !reflect.DeepEqual(applied, []string{"a"}) {
		t.Fatalf("applied %v err %v", applied, err)
	}
	r, _ := LoadReceipt(e.StateDir)
	if len(r.Files) != 2 || r.Files[1].Path != "/x/b" {
		t.Fatalf("partial apply of b not recorded: %+v", r)
	}
}

func TestRunNonInteractiveYesOnlyShellAndBackground(t *testing.T) {
	e, out := testEnv(t, "", false)
	var log []string
	applied, err := Run(e, steps(&log, "a", "shell-hook", "background"), Options{Yes: true})
	if err != nil || !reflect.DeepEqual(applied, []string{"shell-hook", "background"}) {
		t.Fatalf("applied %v err %v", applied, err)
	}
	if o := out.String(); !strings.Contains(o, "– a needs an interactive terminal; run oci-assume setup") {
		t.Fatalf("output %q", o)
	}
}

func TestRunAllDoneNonInteractiveSucceeds(t *testing.T) {
	e, out := testEnv(t, "", false)
	var log []string
	s := []Step{&fakeStep{id: "shell-hook", done: true, log: &log}, &fakeStep{id: "oci-cli", done: true, tty: true, log: &log}}
	if _, err := Run(e, s, Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), doneLine+"\n") {
		t.Fatalf("output %q", out.String())
	}
}

func TestRunNeedsTTYPromptsWhenInteractive(t *testing.T) {
	e, out := testEnv(t, "\n", true)
	var log []string
	applied, err := Run(e, []Step{&fakeStep{id: "oci-cli", tty: true, log: &log}}, Options{})
	if err != nil || !reflect.DeepEqual(applied, []string{"oci-cli"}) || !strings.Contains(out.String(), "do oci-cli [Y/n]") {
		t.Fatalf("applied %v err %v out %q", applied, err, out.String())
	}
}

func TestRunAllDeclinedNothingChanged(t *testing.T) {
	e, out := testEnv(t, "n\n", true)
	var log []string
	if _, err := Run(e, steps(&log, "a"), Options{}); err != nil {
		t.Fatal(err)
	}
	if o := out.String(); !strings.HasSuffix(o, "Nothing changed.\n") || strings.Contains(o, "Done.") {
		t.Fatalf("output %q", o)
	}
}

func TestRunMixedListsDeclined(t *testing.T) {
	e, out := testEnv(t, "y\nn\n", true)
	var log []string
	if _, err := Run(e, steps(&log, "a", "b"), Options{}); err != nil {
		t.Fatal(err)
	}
	if o := out.String(); !strings.Contains(o, "Declined: b") || !strings.HasSuffix(o, doneLine+"\n") {
		t.Fatalf("output %q", o)
	}
}

func writeCorrupt(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunCorruptReceiptSuggestsUninstall(t *testing.T) {
	e, _ := testEnv(t, "", true)
	writeCorrupt(t, e.StateDir)
	var log []string
	if _, err := Run(e, steps(&log, "a"), Options{Yes: true}); err == nil || !strings.Contains(err.Error(), "oci-assume uninstall") || len(log) != 0 {
		t.Fatalf("err %v log %v", err, log)
	}
	if _, err := Run(e, steps(&log, "a"), Options{DryRun: true}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
}

func TestUninstallCorruptReceiptWarnsAndProceeds(t *testing.T) {
	e, out := testEnv(t, "", false)
	writeCorrupt(t, e.StateDir)
	var log []string
	if err := Uninstall(e, steps(&log, "a", "b"), false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(log, []string{"undo b", "undo a"}) || !strings.Contains(out.String(), "warning") {
		t.Fatalf("log %v out %q", log, out.String())
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("corrupt receipt kept: %v", err)
	}
}

func TestUninstallUndoErrorKeepsReceiptAndSkipsPurge(t *testing.T) {
	e, _ := testEnv(t, "", false)
	if err := SaveReceipt(e.StateDir, Receipt{Version: "1"}); err != nil {
		t.Fatal(err)
	}
	var log []string
	s := []Step{&fakeStep{id: "a", log: &log}, &fakeStep{id: "b", undoErr: errors.New("busy"), log: &log}}
	err := Uninstall(e, s, true)
	if err == nil || !strings.Contains(err.Error(), "b: busy") || !reflect.DeepEqual(log, []string{"undo b", "undo a"}) {
		t.Fatalf("err %v log %v", err, log)
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "receipt.json")); err != nil {
		t.Fatalf("receipt not kept: %v", err)
	}
}

func TestUninstallPurgeRefusesDangerousDirs(t *testing.T) {
	e, _ := testEnv(t, "", false)
	root := string(filepath.Separator)
	if v := filepath.VolumeName(e.Home); v != "" {
		root = v + root
	}
	for _, dir := range []string{e.Home, e.Home + string(filepath.Separator), root} {
		e.StateDir = dir
		var log []string
		if err := Uninstall(e, steps(&log, "a"), true); err == nil || !strings.Contains(err.Error(), "refusing") || len(log) != 0 {
			t.Fatalf("%s: err %v log %v", dir, err, log)
		}
	}
	if _, err := os.Stat(e.Home); err != nil {
		t.Fatal(err)
	}
}

func TestEnvContextDefaults(t *testing.T) {
	if (&Env{}).Context() == nil {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if (&Env{Ctx: ctx}).Context().Err() == nil {
		t.Fatal("Ctx not used")
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
