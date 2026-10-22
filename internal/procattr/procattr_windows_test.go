//go:build windows

package procattr

import (
	"os/exec"
	"testing"
)

func TestHideSetsNoWindow(t *testing.T) {
	cmd := exec.Command("cmd")
	Hide(cmd)
	a := cmd.SysProcAttr
	if a == nil || !a.HideWindow || a.CreationFlags&createNoWindow == 0 {
		t.Fatalf("%+v", a)
	}
	cmd = exec.Command("cmd")
	hideIf(cmd, true)
	if cmd.SysProcAttr != nil {
		t.Fatalf("console attached but hidden: %+v", cmd.SysProcAttr)
	}
	hideIf(cmd, false)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("%+v", cmd.SysProcAttr)
	}
}
