package picker

import (
	"unicode"
	"unicode/utf8"
)

type keyKind int

const (
	keyRune keyKind = iota
	keyUp
	keyDown
	keyEnter
	keyEsc
	keyCtrlC
	keyBackspace
)

type keyEvent struct {
	kind keyKind
	r    rune
}

// decode turns raw terminal input into keys; an incomplete tail is returned for the next read.
func decode(b []byte) (keys []keyEvent, rest []byte) {
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			n, k, ok := escape(b)
			if n == 0 {
				return keys, b
			}
			if ok {
				keys = append(keys, k)
			}
			b = b[n:]
			continue
		case c == 0x10 || c == 0x0b: // ctrl-p, ctrl-k
			keys = append(keys, keyEvent{kind: keyUp})
		case c == 0x0e || c == 0x0a: // ctrl-n, ctrl-j
			keys = append(keys, keyEvent{kind: keyDown})
		case c == '\r':
			keys = append(keys, keyEvent{kind: keyEnter})
		case c == 0x03:
			keys = append(keys, keyEvent{kind: keyCtrlC})
		case c == 0x7f || c == 0x08:
			keys = append(keys, keyEvent{kind: keyBackspace})
		case c >= utf8.RuneSelf:
			if !utf8.FullRune(b) {
				return keys, b
			}
			r, n := utf8.DecodeRune(b)
			if r != utf8.RuneError && unicode.IsPrint(r) { // invalid bytes are skipped
				keys = append(keys, keyEvent{kind: keyRune, r: r})
			}
			b = b[n:]
			continue
		case c >= 0x20:
			keys = append(keys, keyEvent{kind: keyRune, r: rune(c)})
		}
		b = b[1:]
	}
	return keys, nil
}

// escape parses a sequence starting with ESC: n bytes consumed (0 if incomplete) and an optional key.
// A malformed prefix consumes only itself, so the keys after it still decode.
func escape(b []byte) (n int, k keyEvent, ok bool) {
	if len(b) == 1 {
		return 0, k, false // a lone ESC waits for flush on the next read timeout
	}
	switch c := b[1]; {
	case c == '[': // CSI: parameter bytes 0x20-0x3f, then a final byte 0x40-0x7e
		for i := 2; i < len(b); i++ {
			switch {
			case b[i] >= 0x40 && b[i] <= 0x7e:
				k, ok = arrow(b[i], i == 2)
				return i + 1, k, ok
			case b[i] < 0x20 || b[i] > 0x3f:
				return 2, k, false
			}
		}
		return 0, k, false
	case c == 'O': // SS3: exactly one final byte
		if len(b) < 3 {
			return 0, k, false
		}
		if b[2] >= 0x40 && b[2] <= 0x7e {
			k, ok = arrow(b[2], true)
			return 3, k, ok
		}
		return 2, k, false
	case c < 0x20 || c == 0x7f: // Esc followed by a control key
		return 1, keyEvent{kind: keyEsc}, true
	}
	if !utf8.FullRune(b[1:]) {
		return 0, k, false
	}
	_, size := utf8.DecodeRune(b[1:])
	return 1 + size, k, false // alt+key, including OSC/DCS/APC introducers, is ignored
}

func arrow(final byte, bare bool) (keyEvent, bool) {
	switch {
	case bare && final == 'A':
		return keyEvent{kind: keyUp}, true
	case bare && final == 'B':
		return keyEvent{kind: keyDown}, true
	}
	return keyEvent{}, false
}

// maxPending bounds the carried-over bytes; real sequences are far shorter.
const maxPending = 32

// feed decodes pending plus new input and caps what carries over.
func feed(pending, in []byte) ([]keyEvent, []byte) {
	keys, rest := decode(append(pending, in...))
	if len(rest) > maxPending {
		rest = nil
	}
	return keys, rest
}

// flush resolves pending bytes after a read timeout: a lone ESC is Esc, the rest is dropped.
func flush(pending []byte) []keyEvent {
	if len(pending) == 1 && pending[0] == 0x1b {
		return []keyEvent{{kind: keyEsc}}
	}
	return nil
}
