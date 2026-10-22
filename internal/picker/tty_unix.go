//go:build !windows

package picker

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const pollEvery = 100 * time.Millisecond

// pollsInput: Read times out, so a trailing ESC can wait for the rest of its sequence.
const pollsInput = true

type unixConsole struct {
	f   *os.File
	fd  int
	old *term.State
}

// openConsole puts /dev/tty in raw mode; the picker never touches stdin or stdout.
func openConsole() (console, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd()) //nolint:gosec // fd fits in int
	old, err := term.MakeRaw(fd)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &unixConsole{f: f, fd: fd, old: old}, nil
}

// Read waits up to pollEvery for input, so the loop can see signals and resizes.
func (c *unixConsole) Read(p []byte) (int, error) {
	var set unix.FdSet
	set.Set(c.fd)
	tv := unix.NsecToTimeval(pollEvery.Nanoseconds())
	n, err := unix.Select(c.fd+1, &set, nil, nil, &tv)
	if errors.Is(err, unix.EINTR) || (err == nil && n == 0) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return c.f.Read(p)
}

func (c *unixConsole) Write(p []byte) (int, error) { return c.f.Write(p) }

func (c *unixConsole) Size() (int, int, bool) {
	w, h, err := term.GetSize(c.fd)
	return w, h, err == nil
}

func (c *unixConsole) Close() error {
	err := term.Restore(c.fd, c.old)
	return errors.Join(err, c.f.Close())
}
