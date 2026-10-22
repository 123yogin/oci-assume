//go:build !windows

package setup

// parentProcessName is Windows-only; unix asks ps via Env.Exec.
func parentProcessName() (parent, grandparent string) { return "", "" }
