//go:build !windows

package setup

import "errors"

// parentProcessName is Windows-only; unix asks ps via Env.Exec.
func parentProcessName() (string, error) { return "", errors.New("not windows") }
