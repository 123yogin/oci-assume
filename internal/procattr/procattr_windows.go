//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000 // CREATE_NO_WINDOW

var getConsoleWindow = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow")

// Hide runs cmd without a console window.
func Hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// HideIfNoConsole hides cmd only when this process has no console (e.g. the windowless helper).
func HideIfNoConsole(cmd *exec.Cmd) {
	hwnd, _, _ := getConsoleWindow.Call()
	hideIf(cmd, hwnd != 0)
}

func hideIf(cmd *exec.Cmd, hasConsole bool) {
	if !hasConsole {
		Hide(cmd)
	}
}
