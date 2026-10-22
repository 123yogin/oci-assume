// Package picker offers an inline, filterable profile chooser.
//
// It never queries the terminal: no colour or cursor-position requests.
package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/123yogin/oci-assume/internal/ociconfig"
)

var (
	ErrNoTTY     = errors.New("no terminal available for the profile picker")
	ErrCancelled = errors.New("profile selection cancelled")
	// ErrNewProfile means the user chose the "+ New profile…" row.
	ErrNewProfile = errors.New("new profile requested")
)

// Item is one selectable profile with its remaining session lifetime.
type Item struct {
	Profile   ociconfig.Profile
	Remaining time.Duration
	Active    bool // the profile currently in $OCI_CLI_PROFILE
	New       bool // the "+ New profile…" row; Profile is ignored
}

// rank orders the active profile, valid sessions, API-key profiles, then expired ones.
func rank(it Item) int {
	switch {
	case it.New:
		return 4
	case it.Active:
		return 0
	case !it.Profile.IsSession():
		return 2
	case it.Remaining > 0:
		return 1
	}
	return 3
}

// Sort orders items by rank, then by name.
func Sort(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := rank(items[i]), rank(items[j])
		if ri != rj {
			return ri < rj
		}
		return items[i].Profile.Name < items[j].Profile.Name
	})
}

// console is the raw-mode terminal; Read returns 0, nil on a poll timeout.
type console interface {
	io.ReadWriter
	Size() (w, h int, ok bool)
	Close() error // restores the terminal mode
	// Polls reports whether Read times out; if not, a trailing lone ESC is Esc at once.
	Polls() bool
}

// Seams for tests: the TTY check, the terminal, and signal delivery.
var (
	isTerminal = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) } //nolint:gosec // fd fits in int
	openTTY    = openConsole
	notify     = func() (<-chan os.Signal, func()) {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		return c, func() { signal.Stop(c) }
	}
)

// colorEnabled honours NO_COLOR and dumb terminals; it never queries the terminal.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	t := os.Getenv("TERM")
	return t != "dumb" && (t != "" || runtime.GOOS == "windows")
}

// Pick shows the picker inline on the controlling terminal, never stdout,
// and returns the chosen profile name.
func Pick(items []Item, in *os.File) (name string, err error) {
	if in == nil || !isTerminal(in) {
		return "", ErrNoTTY
	}
	if len(items) == 0 {
		return "", errors.New("no profiles to pick from")
	}
	c, err := openTTY()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoTTY, err)
	}
	s := &screen{c: c}
	stop := func() {}
	defer func() {
		if r := recover(); r != nil {
			name, err = "", fmt.Errorf("profile picker crashed: %v", r)
		}
		s.clear()
		_ = c.Close()
		stop()
	}()
	var sigs <-chan os.Signal
	sigs, stop = notify()
	s.m = newModel(items, colorEnabled())
	return s.run(sigs)
}

// screen draws frames in place and remembers how many lines are on screen.
type screen struct {
	c     console
	m     model
	lines int
}

func (s *screen) run(sigs <-chan os.Signal) (string, error) {
	s.fit()
	s.draw()
	buf := make([]byte, 256)
	var pending []byte
	for {
		select {
		case sig := <-sigs:
			return "", fmt.Errorf("%w: %v", ErrCancelled, sig)
		default:
		}
		n, err := s.c.Read(buf)
		if errors.Is(err, io.EOF) {
			return "", ErrCancelled
		}
		if err != nil {
			return "", fmt.Errorf("reading terminal: %w", err)
		}
		dirty := s.fit()
		var keys []keyEvent
		if n == 0 {
			keys, pending = flush(pending), nil
		} else if keys, pending = feed(pending, buf[:n]); !s.c.Polls() && len(pending) == 1 && pending[0] == 0x1b {
			keys, pending = append(keys, flush(pending)...), nil // no timeout will come to flush it
		}
		for _, k := range keys {
			if s.m.update(k) {
				if s.m.cancelled {
					return "", ErrCancelled
				}
				if s.m.newProfile {
					return "", ErrNewProfile
				}
				return s.m.choice, nil
			}
			dirty = true
		}
		if dirty {
			s.draw()
		}
	}
}

// fit picks up a terminal resize and reports whether it changed.
func (s *screen) fit() bool {
	w, h, ok := s.c.Size()
	if !ok || (w == s.m.width && h == s.m.height) {
		return false
	}
	s.m.resize(w, h)
	return true
}

// home moves to the first line of the current frame and erases it.
func (s *screen) home() string {
	if s.lines == 0 {
		return ""
	}
	up := ""
	if s.lines > 1 {
		up = fmt.Sprintf("\x1b[%dA", s.lines-1)
	}
	return "\r" + up + "\x1b[J"
}

func (s *screen) draw() {
	v := s.m.View()
	out := "\x1b[?25l" + s.home() + strings.ReplaceAll(v, "\n", "\r\n")
	s.lines = strings.Count(v, "\n") + 1
	_, _ = io.WriteString(s.c, out)
}

func (s *screen) clear() {
	_, _ = io.WriteString(s.c, s.home()+"\x1b[?25h")
	s.lines = 0
}
