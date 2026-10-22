package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	policyTimeout = 30 * time.Second
	// policyPrevFile holds the CurrentUser policy setup replaced; it exists only if setup changed it.
	policyPrevFile = "exec-policy.prev"
	// policyScript prints "Scope=Policy" per scope, then the effective policy.
	policyScript = `Get-ExecutionPolicy -List | ForEach-Object { "$($_.Scope)=$($_.ExecutionPolicy)" }; "Effective=$(Get-ExecutionPolicy)"`
)

type execPolicyStep struct{}

// NewExecPolicyStep returns the Windows execution-policy step.
func NewExecPolicyStep() Step { return execPolicyStep{} }

func (execPolicyStep) ID() string     { return "exec-policy" }
func (execPolicyStep) NeedsTTY() bool { return false }

// policies maps each scope (plus "Effective") to its policy; missing scopes read as Undefined.
type policies map[string]string

func (p policies) get(scope string) string {
	if v := p[strings.ToLower(scope)]; v != "" {
		return v
	}
	return "Undefined"
}

// blocked reports whether the effective policy stops the profile script from loading.
func (p policies) blocked() bool {
	eff := strings.ToLower(p.get("Effective"))
	return eff == "restricted" || eff == "allsigned"
}

// gpo names the Group Policy scope that is set, or "".
func (p policies) gpo() string {
	for _, s := range []string{"MachinePolicy", "UserPolicy"} {
		if !strings.EqualFold(p.get(s), "Undefined") {
			return s
		}
	}
	return ""
}

func powershell(e *Env, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), policyTimeout)
	defer cancel()
	out, err := e.Exec(ctx, "powershell", "-NoProfile", "-Command", command)
	if err != nil {
		return out, fmt.Errorf("powershell %s: %w: %s", command, err, strings.TrimSpace(out))
	}
	return out, nil
}

func readPolicies(e *Env) (policies, error) {
	out, err := powershell(e, policyScript)
	if err != nil {
		return nil, err
	}
	p := policies{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			p[strings.ToLower(k)] = v
		}
	}
	return p, nil
}

func (execPolicyStep) Done(e *Env) (bool, error) {
	if e.GOOS != "windows" {
		return true, nil
	}
	p, err := readPolicies(e)
	if err != nil {
		return false, err
	}
	return !p.blocked(), nil
}

func (execPolicyStep) Describe(e *Env) string {
	p, err := readPolicies(e)
	if err != nil {
		return "PowerShell execution policy: could not be read (" + err.Error() + ")"
	}
	if g := p.gpo(); g != "" {
		return fmt.Sprintf("PowerShell execution policy is %s, enforced by Group Policy (%s); setup cannot change it and the shell hook may not load. Ask your administrator to allow RemoteSigned.", p.get("Effective"), g)
	}
	return fmt.Sprintf("PowerShell execution policy is %s, which blocks the profile hook → run `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`?", p.get("Effective"))
}

func (execPolicyStep) Apply(e *Env, _ *Receipt) error {
	p, err := readPolicies(e)
	if err != nil {
		return err
	}
	if g := p.gpo(); g != "" {
		return fmt.Errorf("%w: Group Policy (%s) enforces %s; the shell hook may not load", ErrSkipped, g, p.get("Effective"))
	}
	if !p.blocked() {
		return nil
	}
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	prev := filepath.Join(e.StateDir, policyPrevFile)
	if err := os.WriteFile(prev, []byte(p.get("CurrentUser")), 0o600); err != nil {
		return err
	}
	if _, err := powershell(e, "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force"); err != nil {
		_ = os.Remove(prev)
		return err
	}
	return nil
}

// Undo restores the CurrentUser policy recorded by Apply; without a record it changes nothing.
func (execPolicyStep) Undo(e *Env, _ *Receipt) error {
	prev := filepath.Join(e.StateDir, policyPrevFile)
	b, err := os.ReadFile(prev) // #nosec G304 -- fixed name under the state dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	old := strings.TrimSpace(string(b))
	if !validPolicy(old) {
		return fmt.Errorf("unexpected recorded policy %q in %s", old, prev)
	}
	if _, err := powershell(e, "Set-ExecutionPolicy "+old+" -Scope CurrentUser -Force"); err != nil {
		return err
	}
	return os.Remove(prev)
}

// validPolicy guards the recorded value before it is put back into a command line.
func validPolicy(s string) bool {
	switch strings.ToLower(s) {
	case "undefined", "restricted", "allsigned", "remotesigned", "unrestricted", "bypass", "default":
		return true
	}
	return false
}
