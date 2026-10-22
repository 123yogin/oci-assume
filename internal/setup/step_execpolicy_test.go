package setup

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// policyList renders the detection script's output for the given scopes.
func policyList(machine, user, current, effective string) string {
	return "MachinePolicy=" + machine + "\r\nUserPolicy=" + user + "\r\nProcess=Undefined\r\n" +
		"CurrentUser=" + current + "\r\nLocalMachine=Undefined\r\nEffective=" + effective + "\r\n"
}

// fakePowershell answers the detection script with list and records Set-ExecutionPolicy calls.
func fakePowershell(e *Env, list *string, sets *[]string) {
	e.GOOS = "windows"
	e.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		cmd := args[len(args)-1]
		if name != "powershell" || args[0] != "-NoProfile" {
			return "", errors.New("unexpected " + name)
		}
		if strings.HasPrefix(cmd, "Set-ExecutionPolicy") {
			*sets = append(*sets, cmd)
			return "", nil
		}
		return *list, nil
	}
}

func TestExecPolicyDetectRestricted(t *testing.T) {
	e, _ := testEnv(t, "", true)
	list := policyList("Undefined", "Undefined", "Undefined", "Restricted")
	var sets []string
	fakePowershell(e, &list, &sets)
	s := NewExecPolicyStep()
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("restricted: done %v %v", done, err)
	}
	if !strings.Contains(s.Describe(e), "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser") {
		t.Fatalf("describe %q", s.Describe(e))
	}
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 || sets[0] != "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force" {
		t.Fatalf("sets %v", sets)
	}
	for _, eff := range []string{"RemoteSigned", "Unrestricted", "Bypass"} {
		list = policyList("Undefined", "Undefined", "Undefined", eff)
		if done, err := s.Done(e); !done || err != nil {
			t.Fatalf("%s: done %v %v", eff, done, err)
		}
	}
	list = policyList("Undefined", "Undefined", "Undefined", "AllSigned")
	if done, _ := s.Done(e); done {
		t.Fatal("AllSigned counted as done")
	}
}

func TestExecPolicyGPOSkips(t *testing.T) {
	e, _ := testEnv(t, "", true)
	list := policyList("AllSigned", "Undefined", "Undefined", "AllSigned")
	var sets []string
	fakePowershell(e, &list, &sets)
	s := NewExecPolicyStep()
	if done, _ := s.Done(e); done {
		t.Fatal("GPO case must still be reported")
	}
	if d := s.Describe(e); !strings.Contains(d, "Group Policy") {
		t.Fatalf("describe %q", d)
	}
	if err := s.Apply(e, &Receipt{}); !errors.Is(err, ErrSkipped) || len(sets) != 0 {
		t.Fatalf("err %v sets %v", err, sets)
	}
}

func TestExecPolicyUndoRestores(t *testing.T) {
	e, _ := testEnv(t, "", true)
	list := policyList("Undefined", "Undefined", "AllSigned", "AllSigned")
	var sets []string
	fakePowershell(e, &list, &sets)
	s := NewExecPolicyStep()
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[1] != "Set-ExecutionPolicy AllSigned -Scope CurrentUser -Force" {
		t.Fatalf("sets %v", sets)
	}
	// A second undo has nothing recorded and changes nothing.
	if err := s.Undo(e, &Receipt{}); err != nil || len(sets) != 2 {
		t.Fatalf("second undo: %v %v", err, sets)
	}
	// Undo never touches a policy setup did not change.
	sets = nil
	if err := s.Undo(e, &Receipt{}); err != nil || len(sets) != 0 {
		t.Fatalf("undo without apply: %v %v", err, sets)
	}
}

func TestExecPolicyNotWindowsDone(t *testing.T) {
	e, _ := testEnv(t, "", true)
	e.Exec = func(context.Context, string, ...string) (string, error) { t.Fatal("exec on linux"); return "", nil }
	if done, err := NewExecPolicyStep().Done(e); !done || err != nil {
		t.Fatalf("done %v %v", done, err)
	}
}
