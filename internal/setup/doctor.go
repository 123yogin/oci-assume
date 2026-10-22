package setup

import (
	"errors"
	"fmt"
)

// HookCheck resolves sh (detected when empty) and lists its hook files lacking the block; a legacy line counts as present.
func HookCheck(e *Env, sh string) (shell string, missing []string, err error) {
	c := *e
	if sh == "" {
		if sh, err = DetectShell(e, ""); err != nil {
			return "", nil, err
		}
	}
	c.Shell = sh
	_, targets, legacy, err := hookStep{}.plan(&c)
	if err != nil || legacy {
		return sh, nil, err
	}
	for _, t := range targets {
		if ok, _ := targetDone(t); !ok {
			missing = append(missing, t.Path)
		}
	}
	return sh, missing, nil
}

// ReceiptProblems lists receipt entries the disk no longer matches; a corrupt receipt is returned as err.
func ReceiptProblems(e *Env) (Receipt, []string, error) {
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
		return r, nil, err
	}
	var ps []string
	for _, f := range r.Files {
		_, s, _, exists, err := readRC(f.Path)
		switch {
		case err != nil:
			ps = append(ps, fmt.Sprintf("%s: %v", f.Path, err))
		case !exists:
			ps = append(ps, f.Path+" is missing")
		case f.Kind == "block":
			if bs, err := blocks(s); err != nil || len(bs) == 0 {
				ps = append(ps, f.Path+" has no oci-assume block")
			}
		}
	}
	if b := r.Background; b != nil && b.Consented && b.Backend != BackendName(e.GOOS) {
		ps = append(ps, fmt.Sprintf("background backend %q is not this system's %q", b.Backend, BackendName(e.GOOS)))
	}
	return r, ps, nil
}

// ExecPolicyStatus reports whether every PowerShell edition allows the profile; why names a Group Policy block.
func ExecPolicyStatus(e *Env) (ok bool, why string, err error) {
	hs, err := readPolicies(e)
	if err != nil {
		return false, "", err
	}
	fixable, enforced := split(hs)
	if len(enforced) > 0 {
		return false, gpoReason(enforced), nil
	}
	return len(fixable) == 0, "", nil
}

// IsNoShell reports whether err means no shell or PowerShell could be resolved.
func IsNoShell(err error) bool { return errors.Is(err, errNoShell) }
