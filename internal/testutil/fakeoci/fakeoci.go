// Package fakeoci installs a fake oci executable for tests.
package fakeoci

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

//go:embed oci
var shScript []byte

//go:embed oci.cmd
var cmdScript []byte

// Install writes the fake oci into a temp dir, sets the FAKE_* env vars and returns its path.
func Install(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	name, body := "oci", shScript
	if runtime.GOOS == "windows" {
		name, body = "oci.cmd", cmdScript
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_OCI_MODE", mode)
	t.Setenv("FAKE_TOKEN_PATH", filepath.Join(dir, "token"))
	t.Setenv("FAKE_OCI_LOG", filepath.Join(dir, "calls.log"))
	return bin
}
