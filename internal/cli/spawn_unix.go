//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so the shell's job control and hangup never reach it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
