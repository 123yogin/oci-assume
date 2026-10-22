package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookCheck(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	rc := filepath.Join(e.Home, ".zshrc")
	if _, missing, err := HookCheck(e, "zsh"); err != nil || len(missing) != 1 || missing[0] != rc {
		t.Fatalf("missing %v err %v", missing, err)
	}
	apply(t, withShell(e, "zsh"))
	if _, missing, err := HookCheck(e, "zsh"); err != nil || len(missing) != 0 {
		t.Fatalf("after apply: missing %v err %v", missing, err)
	}
	// A legacy unmarked line counts as present.
	write(t, filepath.Join(e.Home, ".bashrc"), "eval \"$(oci-assume init bash)\"\n")
	if _, missing, err := HookCheck(e, "bash"); err != nil || len(missing) != 0 {
		t.Fatalf("legacy: missing %v err %v", missing, err)
	}
	if _, _, err := HookCheck(e, ""); !errors.Is(err, errNoShell) {
		t.Fatalf("no shell: %v", err)
	}
}

func withShell(e *Env, sh string) *Env { e.Shell = sh; return e }

func TestReceiptProblems(t *testing.T) {
	e, _ := hookEnv(t, "linux", nil, nil)
	if _, ps, err := ReceiptProblems(e); err != nil || len(ps) != 0 {
		t.Fatalf("no receipt: %v %v", ps, err)
	}
	r := *apply(t, withShell(e, "zsh"))
	r.Background = &BackgroundEntry{Backend: BackendName("linux"), Consented: true}
	r.Files = append(r.Files, FileEntry{Path: filepath.Join(e.Home, "gone.fish"), Kind: "dropin"})
	if err := SaveReceipt(e.StateDir, r); err != nil {
		t.Fatal(err)
	}
	_, ps, err := ReceiptProblems(e)
	if err != nil || len(ps) != 1 || !strings.Contains(ps[0], "gone.fish") {
		t.Fatalf("problems %v err %v", ps, err)
	}
	write(t, filepath.Join(e.Home, ".zshrc"), "user only\n")
	r.Background.Backend = "launchd"
	if err := SaveReceipt(e.StateDir, r); err != nil {
		t.Fatal(err)
	}
	if _, ps, _ = ReceiptProblems(e); len(ps) != 3 {
		t.Fatalf("problems %v", ps)
	}
	if err := os.WriteFile(filepath.Join(e.StateDir, receiptFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReceiptProblems(e); !errors.Is(err, errCorruptReceipt) {
		t.Fatalf("corrupt: %v", err)
	}
}

func TestExecPolicyStatus(t *testing.T) {
	ok := map[string]string{"powershell " + psArgs(policyScript): "Effective=RemoteSigned\nMachinePolicy=Undefined\nUserPolicy=Undefined\n"}
	e, _ := hookEnv(t, "windows", nil, ok)
	if good, why, err := ExecPolicyStatus(e); err != nil || !good || why != "" {
		t.Fatalf("good %v why %q err %v", good, why, err)
	}
	e, _ = hookEnv(t, "windows", nil, map[string]string{"powershell " + psArgs(policyScript): "Effective=Restricted\nMachinePolicy=Restricted\nUserPolicy=Undefined\n"})
	if good, why, err := ExecPolicyStatus(e); err != nil || good || !strings.Contains(why, "Group Policy") {
		t.Fatalf("gpo: good %v why %q err %v", good, why, err)
	}
	e, _ = hookEnv(t, "windows", nil, map[string]string{"powershell " + psArgs(policyScript): "Effective=Restricted\nMachinePolicy=Undefined\nUserPolicy=Undefined\n"})
	if good, why, err := ExecPolicyStatus(e); err != nil || good || why != "" {
		t.Fatalf("fixable: good %v why %q err %v", good, why, err)
	}
	e, _ = hookEnv(t, "windows", nil, nil)
	if _, _, err := ExecPolicyStatus(e); err == nil {
		t.Fatal("want read error")
	}
}

func psArgs(cmd string) string { return "-NoProfile -Command " + cmd }
