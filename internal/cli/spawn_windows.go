//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008 // DETACHED_PROCESS: no console, so no window flashes

// detach runs the child without a console, outside the terminal's Ctrl+C group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
