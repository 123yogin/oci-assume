//go:build !windows

package procattr

import "os/exec"

// Hide is a no-op off Windows.
func Hide(*exec.Cmd) {}

// HideIfNoConsole is a no-op off Windows.
func HideIfNoConsole(*exec.Cmd) {}
