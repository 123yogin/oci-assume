//go:build windows

package cli

import (
	"os/exec"
	"syscall"
	"testing"
)

func TestDetachHasNoWindow(t *testing.T) {
	cmd := exec.Command("cmd")
	detach(cmd)
	f := cmd.SysProcAttr.CreationFlags
	if f&createNoWindow == 0 || f&syscall.CREATE_NEW_PROCESS_GROUP == 0 || f&0x8 != 0 {
		t.Fatalf("flags %#x", f)
	}
}
