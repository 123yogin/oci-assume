//go:build windows

package picker

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
)

// pollsInput: Read blocks, so a trailing ESC is taken as the Esc key at once.
const pollsInput = false

type winConsole struct {
	in, out *os.File
	inOld   *term.State
	outMode uint32
}

// openConsole opens CONIN$/CONOUT$ with VT input and output, not stdin/stdout.
func openConsole() (console, error) {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	c := &winConsole{in: in, out: out}
	h := windows.Handle(out.Fd())
	if err = windows.GetConsoleMode(h, &c.outMode); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	err = windows.SetConsoleMode(h, c.outMode|windows.ENABLE_PROCESSED_OUTPUT|
		windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	if err == nil {
		// MakeRaw also turns on ENABLE_VIRTUAL_TERMINAL_INPUT, so keys arrive as VT sequences.
		c.inOld, err = term.MakeRaw(int(in.Fd()))
	}
	if err != nil {
		_ = windows.SetConsoleMode(h, c.outMode)
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	return c, nil
}

// Read blocks on the main goroutine; Pick returns right after the deciding key,
// so no reader is left behind to hold the console open.
func (c *winConsole) Read(p []byte) (int, error) { return c.in.Read(p) }

func (c *winConsole) Write(p []byte) (int, error) { return c.out.Write(p) }

func (c *winConsole) Size() (int, int, bool) {
	w, h, err := term.GetSize(int(c.out.Fd()))
	return w, h, err == nil
}

func (c *winConsole) Close() error {
	err := errors.Join(
		windows.SetConsoleMode(windows.Handle(c.out.Fd()), c.outMode),
		term.Restore(int(c.in.Fd()), c.inOld),
	)
	return errors.Join(err, c.in.Close(), c.out.Close())
}
