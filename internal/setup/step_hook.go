package setup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// hookStep writes the marked shell-hook block (or fish drop-in) for the detected shell.
type hookStep struct{}

// NewHookStep returns the shell hook step.
func NewHookStep() Step { return hookStep{} }

func (hookStep) ID() string     { return "shell-hook" }
func (hookStep) NeedsTTY() bool { return false }

// plan resolves the shell and its targets; sh == "" means the shell is unknown and the step is skipped.
func (hookStep) plan(e *Env) (sh string, targets []Target, legacy bool, err error) {
	sh, err = DetectShell(e, e.Shell)
	if errors.Is(err, ErrUnknownShell) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	if targets, err = HookTargets(e, sh); err != nil {
		return "", nil, false, err
	}
	for _, p := range legacyFiles(e, sh, targets) {
		if hasLegacyLine(p) {
			return sh, targets, true, nil
		}
	}
	return sh, targets, false, nil
}

func (s hookStep) Describe(e *Env) string {
	sh, targets, _, err := s.plan(e)
	if err != nil {
		return fmt.Sprintf("Shell hook: %v", err)
	}
	var b strings.Builder
	if sh == "" {
		b.WriteString("Shell hook: could not detect your shell, so no file will be changed.\n")
		b.WriteString("  Add the line for your shell yourself, or re-run with --shell zsh|bash|fish|pwsh:\n")
		for _, x := range []struct{ sh, file string }{
			{"zsh", "~/.zshrc"}, {"bash", "~/.bashrc"},
			{"fish", "~/.config/fish/conf.d/oci-assume.fish"}, {"pwsh", "$PROFILE.CurrentUserAllHosts"},
		} {
			fmt.Fprintf(&b, "    %-4s  %s:  %s\n", x.sh, x.file, hookLine(x.sh))
		}
		return b.String()
	}
	for _, t := range targets {
		if done, _ := targetDone(t); done {
			continue
		}
		if t.Kind == "dropin" {
			fmt.Fprintf(&b, "Shell hook: create %s:\n", tilde(e, t.Path))
			for _, l := range strings.Split(strings.TrimRight(t.Content, "\n"), "\n") {
				fmt.Fprintf(&b, "    %s\n", l)
			}
			continue
		}
		fmt.Fprintf(&b, "Shell hook: add to %s:\n    %s\n", tilde(e, t.Path), markBegin)
		for _, l := range strings.Split(t.Content, "\n") {
			fmt.Fprintf(&b, "    %s\n", l)
		}
		fmt.Fprintf(&b, "    %s\n", markEnd)
	}
	return b.String()
}

func (s hookStep) Done(e *Env) (bool, error) {
	sh, targets, legacy, err := s.plan(e)
	if err != nil || sh == "" {
		return false, err
	}
	if legacy {
		return true, nil
	}
	for _, t := range targets {
		if ok, err := targetDone(t); err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (s hookStep) Apply(e *Env, r *Receipt) error {
	sh, targets, legacy, err := s.plan(e)
	if err != nil || sh == "" || legacy {
		return err
	}
	for _, t := range targets {
		if err := applyTarget(e, t); err != nil {
			return err
		}
		if !hasFile(r, t.Path) {
			r.Files = append(r.Files, FileEntry{Path: t.Path, Kind: t.Kind})
		}
	}
	return nil
}

// Undo removes the receipt's blocks and drop-ins, plus any marked block in the known files (v1.0 or lost receipt).
func (hookStep) Undo(e *Env, r *Receipt) error {
	files := append([]FileEntry(nil), r.Files...)
	for _, sh := range []string{"zsh", "bash", "fish", "pwsh"} {
		ts, _ := HookTargets(e, sh)
		if sh == "bash" {
			ts = append(ts, Target{Path: filepath.Join(e.Home, ".bash_profile"), Kind: "block"})
		}
		for _, t := range ts {
			if !hasFileIn(files, t.Path) {
				files = append(files, FileEntry{Path: t.Path, Kind: t.Kind})
			}
		}
	}
	var errs []error
	var kept []FileEntry
	for _, f := range files {
		if err := undoFile(f); err != nil {
			errs = append(errs, err)
			if hasFile(r, f.Path) {
				kept = append(kept, f)
			}
		}
	}
	r.Files = kept
	return errors.Join(errs...)
}

func hasFile(r *Receipt, p string) bool { return hasFileIn(r.Files, p) }

func hasFileIn(fs []FileEntry, p string) bool {
	for _, f := range fs {
		if f.Path == p {
			return true
		}
	}
	return false
}

// tilde shortens paths under HOME for display.
func tilde(e *Env, p string) string {
	if rel, err := filepath.Rel(e.Home, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return p
}
