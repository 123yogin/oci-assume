// Package gobuild compiles helper binaries for tests, once per test process.
package gobuild

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	mu    sync.Mutex
	built = map[string]string{}
	dirs  []string
)

// Build compiles main package pkg to <name>[.exe] in its own temp dir and returns the path.
func Build(pkg, name string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if p, ok := built[pkg]; ok {
		return p, nil
	}
	dir, err := os.MkdirTemp("", "oci-assume-testbin")
	if err != nil {
		return "", err
	}
	dirs = append(dirs, dir)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	if b, err := exec.Command("go", "build", "-o", out, pkg).CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %v\n%s", pkg, err, b)
	}
	built[pkg] = out
	return out, nil
}

// Cleanup removes every built binary; call it from TestMain after m.Run.
func Cleanup() {
	mu.Lock()
	defer mu.Unlock()
	for _, d := range dirs {
		_ = os.RemoveAll(d)
	}
	dirs, built = nil, map[string]string{}
}
