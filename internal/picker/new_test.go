package picker

import (
	"errors"
	"os"
	"testing"
)

func withNew(items []Item) []Item { return append([]Item{{New: true}}, items...) }

func TestPickerNewProfileRow(t *testing.T) {
	m := newTestModel(withNew(fixture()), 100, 40)
	if last := m.items[len(m.items)-1]; !last.New {
		t.Fatalf("new row not sorted last: %+v", m.items)
	}
	golden(t, "newprofile-unselected", m.View())
	golden(t, "newprofile", send(m, key(keyUp)).View())
	// A query matching no profile parks the cursor off the new row; Enter waits for ↓.
	m = send(m, typed("zzz")...)
	if len(m.matches) != 1 || !m.items[m.matches[0]].New {
		t.Fatalf("matches %v", m.matches)
	}
	golden(t, "newprofile-nomatch", m.View())
	if m.update(key(keyEnter)) {
		t.Fatal("enter on a parked cursor finished the picker")
	}
	m = send(m, key(keyDown))
	golden(t, "newprofile-nomatch-selected", m.View())
	if !m.update(key(keyEnter)) || !m.newProfile || m.choice != "" {
		t.Fatalf("enter: newProfile %v choice %q", m.newProfile, m.choice)
	}
}

func TestPickNewProfileReturnsSentinel(t *testing.T) {
	c := &fakeConsole{chunks: []string{"\x1b[A", "\r"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := Pick(withNew(fixture()), os.Stdin); !errors.Is(err, ErrNewProfile) {
		t.Fatalf("got %v, want ErrNewProfile", err)
	}
}
