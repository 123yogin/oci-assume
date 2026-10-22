// Package setup plans and applies the one-command onboarding steps and records them in a receipt.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Env is everything a step touches outside the process, so tests can inject.
type Env struct {
	GOOS, Home, StateDir, Shell string // Shell resolved later by DetectShell
	In                          io.Reader
	Out                         io.Writer
	Interactive                 bool
	Exec                        func(ctx context.Context, name string, args ...string) (string, error) // combined out
	ExecTTY                     func(ctx context.Context, name string, args ...string) error           // stdio to terminal
	LookPath                    func(string) (string, error)
	Executable                  func() (string, error) // the running binary; nil skips the PATH line
	Getenv                      func(string) string
	Now                         func() time.Time
	Ctx                         context.Context // steps use e.Context() for Exec/ExecTTY
}

// Context returns e.Ctx, or context.Background() when unset.
func (e *Env) Context() context.Context {
	if e.Ctx == nil {
		return context.Background()
	}
	return e.Ctx
}

// Step is one reversible onboarding action.
type Step interface {
	ID() string
	Describe(e *Env) string // exact file+content or command, multi-line ok
	Done(e *Env) (bool, error)
	Apply(e *Env, r *Receipt) error // mutates receipt; caller saves
	Undo(e *Env, r *Receipt) error  // used by uninstall
	NeedsTTY() bool                 // oci-cli, login → true
}

// Options controls Run; Skip is keyed by step ID.
type Options struct {
	Yes, DryRun bool
	Skip        map[string]bool
}

// ErrNeedsYes means a non-interactive run had pending steps but no --yes.
var ErrNeedsYes = errors.New("not running interactively; re-run with --yes to apply")

// ErrSkipped, wrapped as "skipped: <reason>", is a non-fatal Apply result; Run reports it and continues.
var ErrSkipped = errors.New("skipped")

// skipper is an optional Step method: a non-empty reason skips a pending step without prompting.
type skipper interface{ SkipReason(e *Env) string }

const doneLine = "Done. Open a new terminal, then run: oci-assume"

// DefaultSteps returns the steps in order: oci-cli, exec-policy (windows), shell-hook, background, first-login.
func DefaultSteps(e *Env) []Step {
	s := []Step{NewOCICLIStep()}
	if e.GOOS == "windows" {
		s = append(s, NewExecPolicyStep())
	}
	return append(s, NewHookStep(), NewBackgroundStep(), NewLoginStep())
}

// nonTTYSteps are the only steps a non-interactive --yes run applies.
var nonTTYSteps = map[string]bool{"shell-hook": true, "background": true}

// Run walks steps, prompting for each pending one, and saves the receipt after every Apply.
func Run(e *Env, steps []Step, o Options) (applied []string, err error) {
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
		if !errors.Is(err, errCorruptReceipt) || !o.DryRun {
			return nil, receiptHint(err)
		}
		r = Receipt{}
	}
	planOnly := o.DryRun || (!e.Interactive && !o.Yes)
	var declined []string
	pending, unapplied := false, false
	for _, s := range steps {
		id := s.ID()
		if o.Skip[id] {
			fmt.Fprintf(e.Out, "– %s skipped\n", id)
			continue
		}
		done, err := s.Done(e)
		if err != nil {
			return applied, fmt.Errorf("%s: %w", id, err)
		}
		if done {
			fmt.Fprintf(e.Out, "✓ %s already set up\n", id)
			continue
		}
		if sk, ok := s.(skipper); ok {
			if why := sk.SkipReason(e); why != "" {
				fmt.Fprintf(e.Out, "– %s skipped: %s\n", id, why)
				unapplied = true
				continue
			}
		}
		if !e.Interactive && !o.DryRun && (s.NeedsTTY() || !nonTTYSteps[id]) {
			fmt.Fprintf(e.Out, "– %s needs an interactive terminal; run oci-assume setup\n", id)
			unapplied = true
			continue
		}
		pending = true
		desc := strings.TrimRight(s.Describe(e), "\n")
		if planOnly || o.Yes {
			fmt.Fprintln(e.Out, desc)
			if planOnly {
				continue
			}
		} else {
			fmt.Fprintf(e.Out, "%s [Y/n] ", desc)
			yes, err := confirm(e)
			if err != nil {
				fmt.Fprintln(e.Out)
				return applied, err
			}
			if !yes {
				fmt.Fprintf(e.Out, "– %s skipped\n", id)
				declined = append(declined, id)
				continue
			}
		}
		before := cloneReceipt(r)
		aerr := s.Apply(e, &r)
		// Save even on failure: a partial Apply may have recorded what it changed.
		saved, err := updateReceipt(e.Context(), e.StateDir, func(cur *Receipt) bool { mergeStep(cur, before, r); return true })
		if err != nil {
			return applied, errors.Join(aerr, fmt.Errorf("%s: save receipt: %w", id, err))
		}
		r = saved
		if errors.Is(aerr, ErrSkipped) {
			fmt.Fprintf(e.Out, "– %s %v\n", id, aerr)
			unapplied = true
			continue
		}
		if aerr != nil {
			return applied, fmt.Errorf("%s: %w", id, aerr)
		}
		applied = append(applied, id)
		fmt.Fprintf(e.Out, "✓ %s done\n", id)
	}
	if o.DryRun {
		return applied, nil
	}
	if planOnly && pending {
		return applied, ErrNeedsYes
	}
	if len(applied) == 0 && (len(declined) > 0 || unapplied) {
		fmt.Fprintln(e.Out, "Nothing changed.")
	} else {
		if len(declined) > 0 {
			fmt.Fprintf(e.Out, "Declined: %s\n", strings.Join(declined, ", "))
		}
		fmt.Fprintln(e.Out, doneLine)
	}
	if dir := pathWarning(e, steps, o); dir != "" {
		fmt.Fprintf(e.Out, "oci-assume is not on PATH in new shells: add %s to PATH\n", dir)
	}
	return applied, nil
}

// pathWarning returns the binary's dir when new shells will not find it and no done hook step adds it.
func pathWarning(e *Env, steps []Step, o Options) string {
	dir, needed := BinDir(e)
	if !needed {
		return ""
	}
	if !pathLineSafe(dir) {
		return dir
	}
	for _, s := range steps {
		if s.ID() == "shell-hook" && !o.Skip[s.ID()] {
			if done, err := s.Done(e); done && err == nil {
				return ""
			}
		}
	}
	return dir
}

// cloneReceipt copies r deeply enough for mergeStep to diff against.
func cloneReceipt(r Receipt) Receipt {
	c := r
	c.Files = append([]FileEntry(nil), r.Files...)
	if r.Background != nil {
		b := *r.Background
		c.Background = &b
	}
	return c
}

// receiptHint points a corrupt receipt at uninstall, which resets it.
func receiptHint(err error) error {
	if errors.Is(err, errCorruptReceipt) {
		return fmt.Errorf("%w; run oci-assume uninstall to reset it, then oci-assume setup", err)
	}
	return err
}

// confirm reads a [Y/n] answer; Enter means yes, EOF means no, and a cancelled ctx (Ctrl-C) returns its error.
func confirm(e *Env) (bool, error) {
	ctx := e.Context()
	line, err := ReadLine(ctx, e.In)
	if cerr := ctx.Err(); cerr != nil {
		return false, cerr
	}
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true, nil
	}
	return false, nil
}

// Uninstall runs Undo in reverse order under the receipt lock, then deletes the receipt; purge also
// removes the state dir and macOS log. Each change is reported; a failed Undo keeps the receipt for a retry.
func Uninstall(e *Env, steps []Step, purge bool) error {
	if purge {
		if err := safeToPurge(e.StateDir, e.Home); err != nil {
			return err
		}
	}
	release, err := LockReceipt(e.Context(), e.StateDir)
	if err != nil {
		return fmt.Errorf("lock receipt (is another oci-assume setup running?): %w", err)
	}
	defer release()
	r, err := LoadReceipt(e.StateDir)
	if errors.Is(err, errCorruptReceipt) {
		fmt.Fprintf(e.Out, "warning: %v; undoing every step without it\n", err)
		r = Receipt{}
	} else if err != nil {
		return err
	}
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		if err := steps[i].Undo(e, &r); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", steps[i].ID(), err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(append(errs, SaveReceipt(e.StateDir, r))...)
	}
	if ok, err := removeReceipt(e.StateDir); err != nil {
		return err
	} else if ok {
		fmt.Fprintf(e.Out, "Removed the setup receipt %s\n", filepath.Join(e.StateDir, receiptFile))
	}
	if !purge {
		return nil
	}
	release() // the lock file lives in the state dir, which Windows cannot delete while it is locked
	if err := os.RemoveAll(e.StateDir); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "Removed the state directory %s\n", e.StateDir)
	if e.GOOS == "darwin" {
		log := filepath.Join(e.Home, "Library", "Logs", "oci-assume.log")
		err := os.Remove(log)
		if err == nil {
			fmt.Fprintf(e.Out, "Removed %s\n", log)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// safeToPurge refuses to delete home, a filesystem or volume root, or an empty path.
func safeToPurge(dir, home string) error {
	c := filepath.Clean(dir)
	if dir == "" || c == "." || c == filepath.Dir(c) || c == filepath.VolumeName(c) || (home != "" && c == filepath.Clean(home)) {
		return fmt.Errorf("refusing to purge state directory %q; remove it by hand", dir)
	}
	return nil
}
