package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// policyList renders the detection script's output for the given scopes.
func policyList(machine, user, current, effective string) string {
	return "MachinePolicy=" + machine + "\r\nUserPolicy=" + user + "\r\nProcess=Undefined\r\n" +
		"CurrentUser=" + current + "\r\nLocalMachine=Undefined\r\nEffective=" + effective + "\r\n"
}

// fakePS answers the detection script per host from lists and records "host: Set-…" calls.
type fakePS struct {
	lists   map[string]string
	sets    []string
	readErr error
	setErr  error
}

func (f *fakePS) install(e *Env, hosts ...string) {
	e.GOOS = "windows"
	e.LookPath = func(name string) (string, error) {
		for _, h := range hosts {
			if h == name {
				return name, nil
			}
		}
		return "", errors.New("not found")
	}
	e.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		cmd := args[len(args)-1]
		if args[0] != "-NoProfile" {
			return "", errors.New("missing -NoProfile")
		}
		if strings.HasPrefix(cmd, "Set-ExecutionPolicy") {
			f.sets = append(f.sets, name+": "+cmd)
			return "", f.setErr
		}
		if f.readErr != nil {
			return "", f.readErr
		}
		return f.lists[name], nil
	}
}

func TestExecPolicyDetectRestricted(t *testing.T) {
	e, _ := testEnv(t, "", true)
	f := &fakePS{lists: map[string]string{"powershell": policyList("Undefined", "Undefined", "Undefined", "Restricted")}}
	f.install(e, "powershell")
	s := NewExecPolicyStep()
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("restricted: done %v %v", done, err)
	}
	if r := s.(skipper).SkipReason(e); r != "" {
		t.Fatalf("skip reason %q", r)
	}
	if !strings.Contains(s.Describe(e), "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser") {
		t.Fatalf("describe %q", s.Describe(e))
	}
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.sets, []string{"powershell: Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force"}) {
		t.Fatalf("sets %v", f.sets)
	}
	for _, eff := range []string{"RemoteSigned", "Unrestricted", "Bypass"} {
		f.lists["powershell"] = policyList("Undefined", "Undefined", "Undefined", eff)
		if done, err := s.Done(e); !done || err != nil {
			t.Fatalf("%s: done %v %v", eff, done, err)
		}
	}
	f.lists["powershell"] = policyList("Undefined", "Undefined", "Undefined", "AllSigned")
	if done, _ := s.Done(e); done {
		t.Fatal("AllSigned counted as done")
	}
}

func TestExecPolicyPwsh7AlsoSet(t *testing.T) {
	e, _ := testEnv(t, "", true)
	f := &fakePS{lists: map[string]string{
		"powershell": policyList("Undefined", "Undefined", "Undefined", "RemoteSigned"),
		"pwsh":       policyList("Undefined", "Undefined", "Restricted", "Restricted"),
	}}
	f.install(e, "powershell", "pwsh")
	s := NewExecPolicyStep()
	if done, _ := s.Done(e); done {
		t.Fatal("blocked pwsh 7 counted as done")
	}
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"pwsh: Set-ExecutionPolicy RemoteSigned -Scope CurrentUser -Force"}
	if !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("sets %v", f.sets)
	}
}

func TestExecPolicyGPOSkips(t *testing.T) {
	e, out := testEnv(t, "y\n", true)
	f := &fakePS{lists: map[string]string{"powershell": policyList("AllSigned", "Undefined", "Undefined", "AllSigned")}}
	f.install(e, "powershell")
	s := NewExecPolicyStep()
	if done, _ := s.Done(e); done {
		t.Fatal("GPO case must still be reported")
	}
	if err := s.Apply(e, &Receipt{}); !errors.Is(err, ErrSkipped) || len(f.sets) != 0 {
		t.Fatalf("err %v sets %v", err, f.sets)
	}
	// Run reports it without a [Y/n] prompt.
	if _, err := Run(e, []Step{s}, Options{}); err != nil {
		t.Fatal(err)
	}
	if o := out.String(); strings.Contains(o, "[Y/n]") || !strings.Contains(o, "– exec-policy skipped: ") || !strings.Contains(o, "Group Policy") {
		t.Fatalf("output %q", o)
	}
}

func TestExecPolicyReadFailureSkips(t *testing.T) {
	e, out := testEnv(t, "", true)
	f := &fakePS{readErr: errors.New("powershell not runnable")}
	f.install(e, "powershell")
	s := NewExecPolicyStep()
	if done, err := s.Done(e); done || err != nil {
		t.Fatalf("done %v %v", done, err)
	}
	if err := s.Apply(e, &Receipt{}); !errors.Is(err, ErrSkipped) {
		t.Fatalf("apply: %v", err)
	}
	if _, err := Run(e, []Step{s}, Options{Yes: true}); err != nil {
		t.Fatalf("read failure was fatal: %v", err)
	}
	if !strings.Contains(out.String(), "– exec-policy skipped: could not read") {
		t.Fatalf("output %q", out.String())
	}
}

func TestExecPolicyUndoPrintsRestoreCommand(t *testing.T) {
	e, out := testEnv(t, "", true)
	f := &fakePS{lists: map[string]string{
		"powershell": policyList("Undefined", "Undefined", "AllSigned", "AllSigned"),
		"pwsh":       policyList("Undefined", "Undefined", "Undefined", "Restricted"),
	}}
	f.install(e, "powershell", "pwsh")
	s := NewExecPolicyStep()
	if err := s.Apply(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := s.Undo(e, &Receipt{}); err != nil {
		t.Fatal(err)
	}
	if len(f.sets) != 2 {
		t.Fatalf("undo changed the policy: %v", f.sets)
	}
	for _, want := range []string{
		"To restore your previous PowerShell execution policy run: Set-ExecutionPolicy AllSigned -Scope CurrentUser (in powershell)\n",
		"To restore your previous PowerShell execution policy run: Set-ExecutionPolicy Undefined -Scope CurrentUser (in pwsh)\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %q", want, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, policyPrevFile)); !os.IsNotExist(err) {
		t.Fatalf("prev file kept: %v", err)
	}
	// Nothing recorded any more, so a second undo prints nothing.
	out.Reset()
	if err := s.Undo(e, &Receipt{}); err != nil || out.Len() != 0 {
		t.Fatalf("second undo: %v %q", err, out.String())
	}
}

func TestExecPolicyPrevKeptOnSetFailure(t *testing.T) {
	e, _ := testEnv(t, "", true)
	f := &fakePS{lists: map[string]string{"powershell": policyList("Undefined", "Undefined", "Undefined", "Restricted")}, setErr: errors.New("access denied")}
	f.install(e, "powershell")
	s := NewExecPolicyStep()
	if err := s.Apply(e, &Receipt{}); err == nil {
		t.Fatal("want set error")
	}
	b, err := os.ReadFile(filepath.Join(e.StateDir, policyPrevFile))
	if err != nil || string(b) != "powershell=Undefined\n" {
		t.Fatalf("prev %q %v", b, err)
	}
}

func TestExecPolicyNotWindowsDone(t *testing.T) {
	e, _ := testEnv(t, "", true)
	e.Exec = func(context.Context, string, ...string) (string, error) { t.Fatal("exec on linux"); return "", nil }
	if done, err := NewExecPolicyStep().Done(e); !done || err != nil {
		t.Fatalf("done %v %v", done, err)
	}
}
