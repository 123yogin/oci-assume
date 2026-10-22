//go:build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Terminal queries that a non-answering pty would stall on.
var queries = []string{"\x1b]11", "\x1b[6n", "\x1b]10", "\x1b[c"}

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// build compiles the binary once and runs it once, so OS first-launch checks stay out of the timings.
func build(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "oci-assume-tty")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "oci-assume")
		if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build: %v\n%s", err, out)
			return
		}
		_ = exec.Command(binPath, "--help").Run()
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		_ = os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

func testEnv(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	body := "[DEFAULT]\nregion=us-ashburn-1\nkey_file=/k.pem\n\n[dev]\nregion=us-phoenix-1\nsecurity_token_file=" +
		filepath.Join(dir, "tok") + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(), "OCI_CLI_CONFIG_FILE="+cfg, "OCI_ASSUME_STATE_DIR="+dir,
		"TERM=xterm-256color", "NO_COLOR=", "COLORTERM=")
}

// ptyBuf collects everything the child writes to its terminal.
type ptyBuf struct {
	mu  sync.Mutex
	b   bytes.Buffer
	eof chan struct{}
}

func (p *ptyBuf) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.b.Write(b)
}

func (p *ptyBuf) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.b.String()
}

// start runs bin on a pty that never answers queries.
func start(t *testing.T, bin string, env []string, args ...string) (*exec.Cmd, *os.File, *ptyBuf) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = f.Close(); _ = cmd.Process.Kill() })
	buf := &ptyBuf{eof: make(chan struct{})}
	go buf.drain(f)
	return cmd, f, buf
}

// drain copies the pty until EOF or EIO, which follows the child's exit.
func (p *ptyBuf) drain(f *os.File) {
	defer close(p.eof)
	b := make([]byte, 4096)
	for {
		k, err := f.Read(b)
		_, _ = p.Write(b[:k])
		if err != nil {
			return
		}
	}
}

// finish waits for the child and for all of its output, failing after d.
func finish(t *testing.T, cmd *exec.Cmd, buf *ptyBuf, d time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.After(d)
	var err error
	select {
	case err = <-done:
	case <-deadline:
		t.Fatalf("process hung; output %q", buf.String())
	}
	select {
	case <-buf.eof:
	case <-deadline:
		t.Fatalf("pty output never ended; output %q", buf.String())
	}
	return err
}

func waitFor(t *testing.T, buf *ptyBuf, s string) {
	t.Helper()
	for began := time.Now(); !strings.Contains(buf.String(), s); time.Sleep(5 * time.Millisecond) {
		if time.Since(began) > 5*time.Second {
			t.Fatalf("no %q; output %q", s, buf.String())
		}
	}
}

func noQueries(t *testing.T, out string) {
	t.Helper()
	for _, q := range queries {
		if strings.Contains(out, q) {
			t.Errorf("terminal query %q in output: %q", q, out)
		}
	}
}

// cleared asserts the frame of n lines was erased: back to its first line, then ESC[J.
func cleared(t *testing.T, out string, n int) {
	t.Helper()
	want := fmt.Sprintf("\r\x1b[%dA\x1b[J\x1b[?25h", n-1)
	frame := strings.LastIndex(out, "esc cancel")
	if frame < 0 || !strings.Contains(out[frame:], want) {
		t.Errorf("frame not cleared with %q: %q", want, out)
	}
}

func TestCommandsSendNoTerminalQueries(t *testing.T) {
	bin, env := build(t), testEnv(t)
	for _, args := range [][]string{{"version"}, {"status"}} {
		t.Run(args[0], func(t *testing.T) {
			began := time.Now()
			cmd, _, buf := start(t, bin, env, args...)
			if err := finish(t, cmd, buf, 5*time.Second); err != nil {
				t.Errorf("%s: %v; output %q", args[0], err, buf.String())
			}
			if d := time.Since(began); d > time.Second {
				t.Errorf("%s took %v", args[0], d)
			}
			if out := buf.String(); strings.Contains(out, "\x1b") {
				t.Errorf("%s wrote an escape sequence: %q", args[0], out)
			}
		})
	}
}

func TestPickerFirstFrameIsFast(t *testing.T) {
	bin, env := build(t), testEnv(t)
	began := time.Now()
	cmd, f, buf := start(t, bin, env, "--shell", "zsh")
	waitFor(t, buf, "Select an OCI profile")
	if d := time.Since(began); d > time.Second {
		t.Errorf("first frame took %v", d)
	}
	waitFor(t, buf, "esc cancel")
	noQueries(t, buf.String())
	if _, err := f.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	var ee *exec.ExitError
	if err := finish(t, cmd, buf, 3*time.Second); !errors.As(err, &ee) {
		t.Errorf("esc should exit non-zero, got %v", err)
	}
	cleared(t, buf.String(), 6)
}

func TestPickerSelectsOnRealPty(t *testing.T) {
	bin, env := build(t), testEnv(t)
	cmd, f, buf := start(t, bin, env, "--shell", "zsh")
	waitFor(t, buf, "esc cancel")
	// DEFAULT (api-key) ranks above the expired session, so enter picks it without a login.
	if _, err := f.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if err := finish(t, cmd, buf, 3*time.Second); err != nil {
		t.Fatalf("exit %v; output %q", err, buf.String())
	}
	out := buf.String()
	cleared(t, out, 6)
	if i := strings.Index(out, "OCI_CLI_PROFILE='DEFAULT'"); i < strings.LastIndex(out, "\x1b[J") {
		t.Errorf("export missing or before the clear: %q", out)
	}
}
