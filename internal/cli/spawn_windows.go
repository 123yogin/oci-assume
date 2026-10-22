//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000 // CREATE_NO_WINDOW: a hidden console, so nothing flashes

// detach runs the child with no visible window, outside the terminal's Ctrl+C group.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | createNoWindow}
}
