// Package fakeoci installs a fake oci executable for tests.
package fakeoci

import (
	"path/filepath"
	"testing"

	"github.com/123yogin/oci-assume/internal/testutil/gobuild"
)

// Install sets the FAKE_* env vars to a fresh temp dir and returns the Go-built fake's path.
// The sh fake beside this file is only for the container shell tests.
func Install(t *testing.T, mode string) string {
	t.Helper()
	bin, err := gobuild.Build("github.com/123yogin/oci-assume/internal/testutil/fakeoci/cmd/oci", "oci")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("FAKE_OCI_MODE", mode)
	t.Setenv("FAKE_TOKEN_PATH", filepath.Join(dir, "token"))
	t.Setenv("FAKE_OCI_LOG", filepath.Join(dir, "calls.log"))
	return bin
}

// Cleanup removes the built fake; call it from TestMain after m.Run.
func Cleanup() { gobuild.Cleanup() }
