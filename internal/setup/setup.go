// Package setup plans and applies the one-command onboarding steps and records them in a receipt.
package setup

import (
	"bufio"
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
	Getenv                      func(string) string
	Now                         func() time.Time
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

const doneLine = "Done. Open a new terminal, then run: oci-assume"

// DefaultSteps returns the steps in order: oci-cli, exec-policy (windows), shell-hook, background, first-login.
func DefaultSteps(e *Env) []Step {
	s := []Step{NewOCICLIStep()}
	if e.GOOS == "windows" {
		s = append(s, NewExecPolicyStep())
	}
	return append(s, NewHookStep(), NewBackgroundStep(), NewLoginStep())
}

// Run walks steps, prompting for each pending one, and saves the receipt after every Apply.
func Run(e *Env, steps []Step, o Options) (applied []string, err error) {
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
		return nil, err
	}
	planOnly := o.DryRun || (!e.Interactive && !o.Yes)
	var in io.Reader = strings.NewReader("")
	if e.In != nil {
		in = e.In
	}
	br := bufio.NewReader(in)
	pending := false
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
		if s.NeedsTTY() && !e.Interactive {
			fmt.Fprintf(e.Out, "– %s needs an interactive terminal; run oci-assume setup\n", id)
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
			if !confirm(br) {
				fmt.Fprintf(e.Out, "– %s skipped\n", id)
				continue
			}
		}
		if err := s.Apply(e, &r); errors.Is(err, ErrSkipped) {
			fmt.Fprintf(e.Out, "– %s %v\n", id, err)
			continue
		} else if err != nil {
			return applied, fmt.Errorf("%s: %w", id, err)
		}
		r.Version = receiptVersion
		if err := SaveReceipt(e.StateDir, r); err != nil {
			return applied, fmt.Errorf("%s: save receipt: %w", id, err)
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
	fmt.Fprintln(e.Out, doneLine)
	return applied, nil
}

// confirm reads a [Y/n] answer; Enter means yes, EOF means no.
func confirm(br *bufio.Reader) bool {
	line, err := br.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// Uninstall runs Undo in reverse order, then deletes the receipt; purge also removes the state dir and macOS log.
// If any Undo fails, the updated receipt is kept so a re-run can retry.
func Uninstall(e *Env, steps []Step, purge bool) error {
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
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
	if err := removeReceipt(e.StateDir); err != nil {
		return err
	}
	if !purge {
		return nil
	}
	if err := os.RemoveAll(e.StateDir); err != nil {
		return err
	}
	if e.GOOS == "darwin" {
		err := os.Remove(filepath.Join(e.Home, "Library", "Logs", "oci-assume.log"))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
