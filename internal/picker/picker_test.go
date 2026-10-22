package picker

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/123yogin/oci-assume/internal/ociconfig"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sess(name, region string, rem time.Duration) Item {
	return Item{Profile: ociconfig.Profile{Name: name, Region: region, SecurityTokenFile: "/t"}, Remaining: rem}
}

func apiKey(name, region string) Item {
	return Item{Profile: ociconfig.Profile{Name: name, Region: region}}
}

func fixture() []Item {
	active := sess("dev", "us-phoenix-1", 48*time.Minute)
	active.Active = true
	return []Item{
		sess("DEFAULT", "uk-london-1", 0),
		apiKey("prod-admin", "eu-frankfurt-1"),
		sess("staging", "us-ashburn-1", 7*time.Minute),
		active,
		sess("sandbox-phx", "us-phoenix-1", 65*time.Minute),
		sess("analytics-team-readonly", "ap-singapore-1", -time.Hour),
	}
}

func newTestModel(items []Item, w, h int) model {
	m := newModel(items, false)
	m.resize(w, h)
	return m
}

func send(m model, keys ...keyEvent) model {
	for _, k := range keys {
		m.update(k)
	}
	return m
}

func key(k keyKind) keyEvent { return keyEvent{kind: k} }

func typed(s string) []keyEvent {
	var out []keyEvent
	for _, r := range s {
		out = append(out, keyEvent{kind: keyRune, r: r})
	}
	return out
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A CRLF checkout must not fail the comparison.
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestGoldenWide(t *testing.T) {
	golden(t, "wide", newTestModel(fixture(), 100, 40).View())
}

func TestGoldenNarrow(t *testing.T) {
	v := newTestModel(fixture(), 40, 40).View()
	for i, l := range strings.Split(v, "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("line %d is %d cols: %q", i, w, l)
		}
	}
	if !strings.Contains(v, "…") {
		t.Error("narrow view has no truncation marker")
	}
	golden(t, "narrow", v)
}

func TestGoldenFiltered(t *testing.T) {
	golden(t, "filtered", send(newTestModel(fixture(), 100, 40), typed("phx")...).View())
}

func TestGoldenNoMatches(t *testing.T) {
	golden(t, "nomatch", send(newTestModel(fixture(), 100, 40), typed("zzz")...).View())
}

func TestGoldenActiveMarker(t *testing.T) {
	// Cursor off the active row, so ">" and "●" sit on different lines.
	golden(t, "active", send(newTestModel(fixture(), 100, 40), key(keyDown)).View())
}

func wideItems() []Item {
	return []Item{
		sess("日本語-prod", "ap-tokyo-1", time.Hour),
		sess("🚀-launch", "us-ashburn-1", 5*time.Minute),
		apiKey("plain", "eu-zurich-1"),
		sess("café", "sa-saopaulo-1", 0),
	}
}

func TestGoldenWideUnicodeAligned(t *testing.T) {
	v := newTestModel(wideItems(), 80, 20).View()
	lines := strings.Split(v, "\n")
	col := -1
	for _, l := range lines[3 : 3+len(wideItems())] {
		i := strings.LastIndex(l, "   ") + 3 // start of the status cell
		if i < 3 {
			t.Fatalf("no columns in %q", l)
		}
		// The status column starts at the same display column on every row.
		c := ansi.StringWidth(l[:i])
		if col >= 0 && c != col {
			t.Errorf("status column %d, want %d in %q", c, col, l)
		}
		col = c
	}
	golden(t, "unicode", v)
}

func names(m model) []string {
	var out []string
	for _, i := range m.matches {
		out = append(out, m.items[i].Profile.Name)
	}
	return out
}

func current(m model) string { return m.items[m.matches[m.cursor]].Profile.Name }

func TestSortOrder(t *testing.T) {
	items := []Item{
		sess("a-expired", "r", 0),
		apiKey("b-key", ""),
		sess("z-ok", "r", time.Minute),
		apiKey("a-key", ""),
		sess("m-ok", "r", time.Hour),
		sess("b-expired", "r", -time.Minute),
		{Profile: ociconfig.Profile{Name: "zz-active", SecurityTokenFile: "/t"}, Active: true},
	}
	Sort(items)
	want := []string{"zz-active", "m-ok", "z-ok", "a-key", "b-key", "a-expired", "b-expired"}
	for i, w := range want {
		if items[i].Profile.Name != w {
			t.Errorf("items[%d]=%q want %q", i, items[i].Profile.Name, w)
		}
	}
}

func TestFilterOrdersByScoreThenRank(t *testing.T) {
	m := send(newTestModel(fixture(), 100, 40), typed("phx")...)
	if got := strings.Join(names(m), ","); got != "sandbox-phx,dev" {
		t.Errorf("phx matches %q", got)
	}
	// Equal scores fall back to rank: the active profile beats name order.
	b := sess("dev-b", "us-x-1", time.Hour)
	b.Active = true
	m = send(newTestModel([]Item{sess("dev-a", "us-x-1", time.Hour), b}, 100, 40), typed("dev")...)
	if got := strings.Join(names(m), ","); got != "dev-b,dev-a" {
		t.Errorf("tie order %q", got)
	}
}

func TestMovementWraps(t *testing.T) {
	m := newTestModel(fixture(), 100, 40)
	if current(m) != "dev" {
		t.Fatalf("start %q", current(m))
	}
	m = send(m, key(keyUp))
	if current(m) != "analytics-team-readonly" {
		t.Errorf("up from top: %q", current(m))
	}
	m = send(m, key(keyDown))
	if current(m) != "dev" {
		t.Errorf("down from bottom: %q", current(m))
	}
}

func TestPlainJKFilter(t *testing.T) {
	m := send(newTestModel(fixture(), 100, 40), typed("jk")...)
	if m.query != "jk" {
		t.Errorf("query %q", m.query)
	}
	m = send(m, key(keyBackspace))
	if m.query != "j" {
		t.Errorf("after backspace %q", m.query)
	}
}

func TestTypingResetsCursor(t *testing.T) {
	m := send(newTestModel(fixture(), 100, 40), key(keyDown), key(keyDown))
	m = send(m, typed("s")...)
	if m.cursor != 0 {
		t.Errorf("cursor %d after typing", m.cursor)
	}
}

func TestEnterSelects(t *testing.T) {
	m := send(newTestModel(fixture(), 100, 40), key(keyDown))
	if !m.update(key(keyEnter)) {
		t.Fatal("enter did not finish")
	}
	if m.choice != "sandbox-phx" || m.cancelled {
		t.Errorf("choice %q cancelled %v", m.choice, m.cancelled)
	}
}

func TestEnterWithNoMatchesIsNoop(t *testing.T) {
	m := send(newTestModel(fixture(), 100, 40), typed("zzz")...)
	if m.update(key(keyEnter)) || m.choice != "" {
		t.Error("enter with zero matches should do nothing")
	}
}

func TestEscAndCtrlCCancel(t *testing.T) {
	for _, k := range []keyKind{keyEsc, keyCtrlC} {
		m := newTestModel(fixture(), 100, 40)
		if !m.update(key(k)) || !m.cancelled || m.choice != "" {
			t.Errorf("%v: cancelled %v choice %q", k, m.cancelled, m.choice)
		}
	}
}

func TestListHeightCapsAndScrolls(t *testing.T) {
	var items []Item
	for i := 0; i < 15; i++ {
		items = append(items, sess(fmt.Sprintf("p%02d", i), "r", time.Hour))
	}
	m := newTestModel(items, 100, 40)
	if n := m.listHeight(); n != 10 {
		t.Errorf("rows %d want 10", n)
	}
	m = send(m, key(keyUp))
	if !strings.Contains(m.View(), ">  p14") {
		t.Errorf("last row not scrolled into view:\n%s", m.View())
	}
	// Title, filter, header and footer leave 3 rows on a 7-line terminal.
	m.resize(100, 7)
	if n := m.listHeight(); n != 3 {
		t.Errorf("rows %d want 3", n)
	}
	if lines := strings.Count(m.View(), "\n") + 1; lines != 7 {
		t.Errorf("view has %d lines", lines)
	}
	if !strings.Contains(m.View(), ">  p14") {
		t.Errorf("cursor lost after resize:\n%s", m.View())
	}
	if n := newTestModel(items[:3], 100, 40).listHeight(); n != 3 {
		t.Errorf("rows %d want 3", n)
	}
}

func TestShortTerminalDropsHeaderAndFooterFirst(t *testing.T) {
	for h, want := range map[int][]string{
		4: {title, "> type to filter", ">● dev", "sandbox-phx"},
		3: {title, "> type to filter", ">● dev"},
		2: {"> type to filter", ">● dev"},
	} {
		v := newTestModel(fixture(), 80, h).View()
		lines := strings.Split(v, "\n")
		if len(lines) != len(want) {
			t.Errorf("h=%d: %d lines:\n%s", h, len(lines), v)
			continue
		}
		for i, w := range want {
			if !strings.Contains(lines[i], w) {
				t.Errorf("h=%d line %d = %q, want %q", h, i, lines[i], w)
			}
		}
		if strings.Contains(v, "PROFILE") || strings.Contains(v, "esc cancel") {
			t.Errorf("h=%d kept header or footer:\n%s", h, v)
		}
	}
}

func TestViewNeverPanicsOrOverflows(t *testing.T) {
	for _, n := range []int{1, 8, 120} {
		var items []Item
		for i := 0; i < n; i++ {
			items = append(items, sess(fmt.Sprintf("profile-%03d-日本", i), "us-phoenix-1", time.Duration(i)*time.Minute))
		}
		for w := 1; w <= 30; w++ {
			for h := 1; h <= 6; h++ {
				for _, q := range []string{"", "zzz"} {
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("n=%d w=%d h=%d q=%q: panic %v", n, w, h, q, r)
							}
						}()
						m := send(newTestModel(items, w, h), typed(q)...)
						m = send(m, key(keyUp))
						for _, l := range strings.Split(m.View(), "\n") {
							if lw := ansi.StringWidth(l); lw > w {
								t.Errorf("n=%d w=%d h=%d: line %d cols %q", n, w, h, lw, l)
							}
						}
					}()
				}
			}
		}
	}
}

func TestColourUsesBasicANSIAndKeepsLayout(t *testing.T) {
	plain := newTestModel(fixture(), 100, 40).View()
	m := newModel(fixture(), true)
	m.resize(100, 40)
	col := m.View()
	if ansi.Strip(col) != plain {
		t.Errorf("colour layout differs from plain:\n%s\n---\n%s", ansi.Strip(col), plain)
	}
	for _, want := range []string{"\x1b[32m48m", "\x1b[33m7m", "\x1b[2mexpired", "\x1b[34mapi-key"} {
		if !strings.Contains(col, want) {
			t.Errorf("missing %q in %q", want, col)
		}
	}
	// Only ANSI-16 foregrounds and attributes: no 256-colour or truecolour.
	if strings.Contains(col, "38;5;") || strings.Contains(col, "38;2;") {
		t.Errorf("non-basic colour in %q", col)
	}
}

func TestColourEnv(t *testing.T) {
	for _, c := range []struct {
		noColor, term string
		want          bool
	}{
		{"", "xterm-256color", true},
		{"1", "xterm-256color", false},
		{"", "dumb", false},
	} {
		t.Setenv("NO_COLOR", c.noColor)
		t.Setenv("TERM", c.term)
		if got := colorEnabled(); got != c.want {
			t.Errorf("NO_COLOR=%q TERM=%q: %v", c.noColor, c.term, got)
		}
	}
}

func TestDecode(t *testing.T) {
	for in, want := range map[string][]keyEvent{
		"\x1b[A\x1bOB":    {key(keyUp), key(keyDown)},
		"\x10\x0b\x0e\n":  {key(keyUp), key(keyUp), key(keyDown), key(keyDown)},
		"\r":              {key(keyEnter)},
		"\x03":            {key(keyCtrlC)},
		"\x7f\x08":        {key(keyBackspace), key(keyBackspace)},
		"aé日🚀":            {{keyRune, 'a'}, {keyRune, 'é'}, {keyRune, '日'}, {keyRune, '🚀'}},
		"\x1b[1;5C\x1bxq": {{keyRune, 'q'}},
		// An escape prefix never swallows the keys that follow it.
		"\x1b[\x03":      {key(keyCtrlC)},
		"\x1b[1\r":       {{keyRune, '1'}, key(keyEnter)},
		"\x1bO\r":        {key(keyEnter)},
		"\x1b]\x03":      {key(keyCtrlC)},
		"\x1bP\x1b[B":    {key(keyDown)},
		"\x1b_\r":        {key(keyEnter)},
		"\x1b\x03":       {key(keyEsc), key(keyCtrlC)},
		"\x1b\x1b[A":     {key(keyEsc), key(keyUp)},
		"\xff\xfea\xe6A": {{keyRune, 'a'}, {keyRune, 'A'}},
	} {
		got, rest := decode([]byte(in))
		if fmt.Sprint(got) != fmt.Sprint(want) || len(rest) != 0 {
			t.Errorf("decode(%q) = %v rest %q, want %v", in, got, rest, want)
		}
	}
	// Split sequences, runes and a trailing ESC carry over to the next read.
	for _, in := range []string{"\x1b", "\x1b[", "\x1b[12", "\x1bO", "\xe6\x97"} {
		got, rest := decode([]byte(in))
		if len(got) != 0 || string(rest) != in {
			t.Errorf("decode(%q) = %v rest %q", in, got, rest)
		}
	}
}

func TestFlushAndCap(t *testing.T) {
	if got := flush([]byte("\x1b")); fmt.Sprint(got) != fmt.Sprint([]keyEvent{key(keyEsc)}) {
		t.Errorf("lone ESC flushed to %v", got)
	}
	if got := flush([]byte("\x1b[12")); len(got) != 0 {
		t.Errorf("partial CSI flushed to %v", got)
	}
	_, rest := feed(nil, []byte("\x1b["+strings.Repeat("1", 100)))
	if len(rest) > maxPending {
		t.Errorf("pending grew to %d bytes", len(rest))
	}
}

func TestSingleLineTerminal(t *testing.T) {
	v := newTestModel(fixture(), 80, 1).View()
	if strings.Contains(v, "\n") || !strings.Contains(v, ">● dev") {
		t.Errorf("h=1 idle: %q", v)
	}
	v = send(newTestModel(fixture(), 80, 1), typed("st")...).View()
	if strings.Contains(v, "\n") || !strings.HasPrefix(v, "> st") {
		t.Errorf("h=1 typing: %q", v)
	}
}

func TestPickNoTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	start := time.Now()
	_, err = Pick([]Item{sess("dev", "us-ashburn-1", time.Hour)}, r)
	if !errors.Is(err, ErrNoTTY) {
		t.Fatalf("got %v, want ErrNoTTY", err)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("took %v", d)
	}
}

// fakeConsole replays chunks; an empty chunk is a read timeout.
// It polls like a unix tty unless blocking is set, so tests behave the same on every OS.
type fakeConsole struct {
	blocking bool
	chunks   []string
	out      bytes.Buffer
	w, h     int
	closed   bool
	panicAt  int // read index that panics, or -1
	onRead   func(i int)
	reads    int
}

func (c *fakeConsole) Read(p []byte) (int, error) {
	i := c.reads
	c.reads++
	if c.onRead != nil {
		c.onRead(i)
	}
	if i == c.panicAt {
		panic("boom")
	}
	if i >= len(c.chunks) {
		return 0, io.EOF
	}
	return copy(p, c.chunks[i]), nil
}

func (c *fakeConsole) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *fakeConsole) Size() (int, int, bool)      { return c.w, c.h, c.w > 0 }
func (c *fakeConsole) Close() error                { c.closed = true; return nil }
func (c *fakeConsole) Polls() bool                 { return !c.blocking }

// fakeTerminal makes Pick run on c and returns a channel that delivers signals to it.
func fakeTerminal(t *testing.T, c *fakeConsole) chan<- os.Signal {
	t.Helper()
	oldIs, oldOpen, oldNotify := isTerminal, openTTY, notify
	t.Cleanup(func() { isTerminal, openTTY, notify = oldIs, oldOpen, oldNotify })
	sigs := make(chan os.Signal, 1)
	isTerminal = func(*os.File) bool { return true }
	openTTY = func() (console, error) { return c, nil }
	notify = func() (<-chan os.Signal, func()) { return sigs, func() {} }
	return sigs
}

func pickCapturingStdout(t *testing.T) (string, []byte, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	name, perr := Pick(fixture(), r)
	os.Stdout = oldStdout
	_ = w.Close()
	leaked, _ := io.ReadAll(r)
	_ = r.Close()
	return name, leaked, perr
}

// assertCleared checks the picker erased itself and restored the terminal.
func assertCleared(t *testing.T, c *fakeConsole, lines int) {
	t.Helper()
	out := c.out.String()
	if !c.closed {
		t.Error("terminal not restored")
	}
	last := strings.LastIndex(out, "esc cancel")
	if last < 0 {
		t.Fatalf("picker not drawn on the terminal: %q", out)
	}
	tail := out[last+len("esc cancel"):]
	if !strings.Contains(tail, "\x1b[J") || strings.TrimSpace(ansi.Strip(tail)) != "" {
		t.Errorf("picker not cleared at exit: %q", tail)
	}
	if want := fmt.Sprintf("\r\x1b[%dA\x1b[J\x1b[?25h", lines-1); !strings.HasSuffix(out, want) {
		t.Errorf("exit sequence %q, want suffix %q", out[max(0, len(out)-24):], want)
	}
}

func TestPickNeverWritesStdout(t *testing.T) {
	// The arrow arrives split across reads, as it can over ssh.
	c := &fakeConsole{chunks: []string{"", "\x1b", "[B", "\r"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	name, leaked, err := pickCapturingStdout(t)
	if err != nil || name != "sandbox-phx" {
		t.Fatalf("Pick = %q, %v", name, err)
	}
	if len(leaked) != 0 {
		t.Errorf("Pick wrote %q to stdout", leaked)
	}
	assertCleared(t, c, 10)
}

func TestPickCancel(t *testing.T) {
	// A lone ESC is the Esc key once a read times out.
	c := &fakeConsole{chunks: []string{"\x1b", "", "x"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := Pick(fixture(), os.Stdin); !errors.Is(err, ErrCancelled) || c.reads != 2 {
		t.Fatalf("got %v after %d reads, want ErrCancelled after 2", err, c.reads)
	}
	assertCleared(t, c, 10)
}

// A console whose Read blocks (Windows) takes a trailing lone ESC as Esc at once.
func TestPickBlockingConsoleEscIsImmediate(t *testing.T) {
	c := &fakeConsole{blocking: true, chunks: []string{"\x1b", "x"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := Pick(fixture(), os.Stdin); !errors.Is(err, ErrCancelled) || c.reads != 1 {
		t.Fatalf("got %v after %d reads, want ErrCancelled after 1", err, c.reads)
	}
	assertCleared(t, c, 10)
}

func TestPickEOFIsCancel(t *testing.T) {
	c := &fakeConsole{w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := Pick(fixture(), os.Stdin); !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled", err)
	}
	assertCleared(t, c, 10)
}

func TestPickEscapePrefixDoesNotFreeze(t *testing.T) {
	c := &fakeConsole{chunks: []string{"\x1b[", "\x03"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := Pick(fixture(), os.Stdin); !errors.Is(err, ErrCancelled) || c.reads != 2 {
		t.Fatalf("got %v after %d reads", err, c.reads)
	}
}

func TestPickRestoresIfSetupPanics(t *testing.T) {
	c := &fakeConsole{w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	notify = func() (<-chan os.Signal, func()) { panic("setup") }
	_, err := Pick(fixture(), os.Stdin)
	if err == nil || errors.Is(err, ErrCancelled) || !c.closed {
		t.Fatalf("got %v, closed %v", err, c.closed)
	}
}

func TestPickRedrawsOnResize(t *testing.T) {
	c := &fakeConsole{chunks: []string{"", "", "\r"}, w: 100, h: 40, panicAt: -1}
	c.onRead = func(i int) {
		if i == 1 {
			c.w = 40
		}
	}
	fakeTerminal(t, c)
	if _, err := Pick(fixture(), os.Stdin); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.out.String(), "eu-frankfur…") {
		t.Errorf("no narrow frame after resize: %q", c.out.String())
	}
}

func TestPickPanicIsAnErrorNotACancel(t *testing.T) {
	c := &fakeConsole{chunks: []string{"", ""}, w: 100, h: 40, panicAt: 1}
	fakeTerminal(t, c)
	name, leaked, err := pickCapturingStdout(t)
	if err == nil || errors.Is(err, ErrCancelled) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Pick = %q, %v; want a panic error", name, err)
	}
	if len(leaked) != 0 {
		t.Errorf("Pick wrote %q to stdout", leaked)
	}
	assertCleared(t, c, 10)
}

func TestPickSignalClearsAndRestores(t *testing.T) {
	for _, sig := range []os.Signal{syscall.SIGTERM, os.Interrupt} {
		c := &fakeConsole{chunks: []string{"", "", ""}, w: 100, h: 40, panicAt: -1}
		sigs := fakeTerminal(t, c)
		c.onRead = func(i int) {
			if i == 1 {
				sigs <- sig
			}
		}
		_, err := Pick(fixture(), os.Stdin)
		if !errors.Is(err, ErrCancelled) || !strings.Contains(err.Error(), sig.String()) {
			t.Fatalf("%v: got %v", sig, err)
		}
		assertCleared(t, c, 10)
	}
}
