//go:build !windows

package procattr

import (
	"os/exec"
	"testing"
)

func TestHideIsNoopOffWindows(t *testing.T) {
	cmd := exec.Command("true")
	Hide(cmd)
	HideIfNoConsole(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("%+v", cmd.SysProcAttr)
	}
}
