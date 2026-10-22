package setup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	policyTimeout = 30 * time.Second
	// policyPrevFile holds "host=policy" lines for each CurrentUser policy setup may have changed.
	policyPrevFile = "exec-policy.prev"
	// policyScript prints "Scope=Policy" per scope, then the effective policy.
	policyScript = `Get-ExecutionPolicy -List | ForEach-Object { "$($_.Scope)=$($_.ExecutionPolicy)" }; "Effective=$(Get-ExecutionPolicy)"`
)

type execPolicyStep struct{}

// NewExecPolicyStep returns the Windows execution-policy step (Windows PowerShell, plus pwsh 7 if installed).
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

// hostPolicy is one PowerShell edition's policies.
type hostPolicy struct {
	host string
	p    policies
}

// psHosts is Windows PowerShell, plus pwsh 7 when it is on PATH; each keeps its own CurrentUser policy.
func psHosts(e *Env) []string {
	h := []string{"powershell"}
	if e.LookPath != nil {
		if _, err := e.LookPath("pwsh"); err == nil {
			h = append(h, "pwsh")
		}
	}
	return h
}

func runPS(e *Env, host, command string) (string, error) {
	ctx, cancel := context.WithTimeout(e.Context(), policyTimeout)
	defer cancel()
	out, err := e.Exec(ctx, host, "-NoProfile", "-Command", command)
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", host, command, err, strings.TrimSpace(out))
	}
	return out, nil
}

func readPolicies(e *Env) ([]hostPolicy, error) {
	var hs []hostPolicy
	for _, h := range psHosts(e) {
		out, err := runPS(e, h, policyScript)
		if err != nil {
			return nil, err
		}
		p := policies{}
		for _, line := range strings.Split(out, "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
				p[strings.ToLower(k)] = v
			}
		}
		hs = append(hs, hostPolicy{h, p})
	}
	return hs, nil
}

// split sorts the blocked hosts into ones setup can fix and ones Group Policy enforces.
func split(hs []hostPolicy) (fixable, enforced []hostPolicy) {
	for _, h := range hs {
		switch {
		case !h.p.blocked():
		case h.p.gpo() != "":
			enforced = append(enforced, h)
		default:
			fixable = append(fixable, h)
		}
	}
	return fixable, enforced
}

func gpoReason(enforced []hostPolicy) string {
	var parts []string
	for _, h := range enforced {
		parts = append(parts, fmt.Sprintf("%s is %s by Group Policy (%s)", h.host, h.p.get("Effective"), h.p.gpo()))
	}
	return strings.Join(parts, "; ") + "; setup cannot change it and the shell hook may not load"
}

// Done is true off Windows, or when no edition's policy blocks the hook; a read failure is not done.
func (execPolicyStep) Done(e *Env) (bool, error) {
	if e.GOOS != "windows" {
		return true, nil
	}
	hs, err := readPolicies(e)
	if err != nil {
		return false, nil
	}
	fixable, enforced := split(hs)
	return len(fixable) == 0 && len(enforced) == 0, nil
}

// SkipReason skips without prompting on a read failure or when only Group Policy blocks the hook.
func (execPolicyStep) SkipReason(e *Env) string {
	hs, err := readPolicies(e)
	if err != nil {
		return "could not read the PowerShell execution policy: " + err.Error()
	}
	if fixable, enforced := split(hs); len(fixable) == 0 && len(enforced) > 0 {
		return gpoReason(enforced)
	}
	return ""
}

func (execPolicyStep) Describe(e *Env) string {
	hs, err := readPolicies(e)
	if err != nil {
		return "PowerShell execution policy could not be read (" + err.Error() + ")"
	}
	fixable, enforced := split(hs)
	var names []string
	for _, h := range fixable {
		names = append(names, fmt.Sprintf("%s (%s)", h.host, h.p.get("Effective")))
	}
	d := "PowerShell execution policy blocks the profile hook in " + strings.Join(names, ", ") +
		" → run `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`?"
	if len(enforced) > 0 {
		d += "\n  note: " + gpoReason(enforced)
	}
	return d
}

func (execPolicyStep) Apply(e *Env, _ *Receipt) error {
	hs, err := readPolicies(e)
	if err != nil {
		return fmt.Errorf("%w: could not read the PowerShell execution policy: %v", ErrSkipped, err)
	}
	fixable, enforced := split(hs)
	if len(fixable) == 0 {
		if len(enforced) > 0 {
			return fmt.Errorf("%w: %s", ErrSkipped, gpoReason(enforced))
		}
		return nil
	}
	prev, err := loadPrev(e)
	if err != nil {
		return err
	}
	for _, h := range fixable {
		if _, ok := prev[h.host]; !ok {
			prev[h.host] = h.p.get("CurrentUser")
		}
	}
	// Recorded before any change and kept on failure, since a failed Set may still have applied.
	if err := savePrev(e, prev); err != nil {
		return err
	}
	for _, h := range fixable {
		if _, err := runPS(e, h.host, "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force"); err != nil {
			return err
		}
	}
	if len(enforced) > 0 {
		fmt.Fprintf(e.Out, "  note: %s\n", gpoReason(enforced))
	}
	return nil
}

// Undo restores each CurrentUser policy recorded by Apply; without a record it changes nothing.
func (execPolicyStep) Undo(e *Env, _ *Receipt) error {
	prev, err := loadPrev(e)
	if err != nil || len(prev) == 0 {
		return err
	}
	hosts := make([]string, 0, len(prev))
	for h := range prev {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		if e.LookPath != nil {
			if _, err := e.LookPath(h); err != nil {
				continue // that edition was uninstalled; nothing to restore
			}
		}
		if _, err := runPS(e, h, "Set-ExecutionPolicy "+prev[h]+" -Scope CurrentUser -Force"); err != nil {
			return err
		}
	}
	return os.Remove(filepath.Join(e.StateDir, policyPrevFile))
}

// loadPrev reads the recorded policies; values are checked before they go back into a command line.
func loadPrev(e *Env) (map[string]string, error) {
	f := filepath.Join(e.StateDir, policyPrevFile)
	b, err := os.ReadFile(f) // #nosec G304 -- fixed name under the state dir
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		h, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || (h != "powershell" && h != "pwsh") || !validPolicy(v) {
			return nil, fmt.Errorf("unexpected line %q in %s", line, f)
		}
		m[h] = v
	}
	return m, nil
}

func savePrev(e *Env, m map[string]string) error {
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for _, h := range []string{"powershell", "pwsh"} {
		if v, ok := m[h]; ok {
			b.WriteString(h + "=" + v + "\n")
		}
	}
	return os.WriteFile(filepath.Join(e.StateDir, policyPrevFile), []byte(b.String()), 0o600)
}

func validPolicy(s string) bool {
	switch strings.ToLower(s) {
	case "undefined", "restricted", "allsigned", "remotesigned", "unrestricted", "bypass", "default":
		return true
	}
	return false
}
