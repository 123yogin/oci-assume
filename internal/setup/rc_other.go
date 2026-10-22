//go:build !windows

package setup

import "errors"

// parentProcessName is Windows-only; unix asks ps via Env.Exec.
func parentProcessName() (parent, grandparent string) { return "", "" }

// NewShellPath is Windows-only; elsewhere new shells build PATH from rc files, so callers use LookPath.
func NewShellPath() (string, error) { return "", errors.New("unsupported") }
