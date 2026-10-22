package refresher

import (
	"errors"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// BinReporter is implemented by backends that can read the binary path from their installed definition.
type BinReporter interface {
	InstalledBin() (string, error)
}

// ErrNoDefinition means no installed definition, or none naming a binary, was found.
var ErrNoDefinition = errors.New("no scheduler definition with a binary path")

// InstalledBin reads ProgramArguments[0] from the plist.
func (l *launchd) InstalledBin() (string, error) {
	b, err := os.ReadFile(l.plist())
	if err != nil {
		return "", err
	}
	_, rest, ok := strings.Cut(string(b), "<key>ProgramArguments</key>")
	if !ok {
		return "", ErrNoDefinition
	}
	_, rest, ok = strings.Cut(rest, "<string>")
	v, _, ok2 := strings.Cut(rest, "</string>")
	if !ok || !ok2 {
		return "", ErrNoDefinition
	}
	return html.UnescapeString(v), nil
}

// InstalledBin reads ExecStart from the service unit, or the binary from the cron line.
func (d *systemd) InstalledBin() (string, error) {
	dir, ok := d.probe()
	if !ok {
		lines, err := d.cronRead()
		if err != nil && !errors.Is(err, errNoCrontab) {
			return "", err
		}
		for _, l := range lines {
			if strings.HasSuffix(strings.TrimSpace(l), cronMarker) {
				return cronBin(l)
			}
		}
		return "", ErrNoDefinition
	}
	b, err := os.ReadFile(filepath.Join(dir, serviceName)) // #nosec G304 -- our own unit file
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, found := strings.CutPrefix(l, "ExecStart="); found {
			return unitUnquote(v)
		}
	}
	return "", ErrNoDefinition
}

// unitUnquote reverses unitQuote for the first word of an ExecStart value.
func unitUnquote(v string) (string, error) {
	if !strings.HasPrefix(v, `"`) {
		return "", ErrNoDefinition
	}
	var b strings.Builder
	for i := 1; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '"':
			return b.String(), nil
		case c == '\\' && i+1 < len(v):
			i++
			b.WriteByte(v[i])
		case (c == '%' || c == '$') && i+1 < len(v) && v[i+1] == c:
			i++
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return "", ErrNoDefinition
}

// cronBin returns the first shell word after the schedule that is an absolute path; cron unescapes \% first.
func cronBin(line string) (string, error) {
	words := shellWords(strings.ReplaceAll(line, `\%`, "%"))
	for _, w := range words[min(5, len(words)):] {
		if strings.HasPrefix(w, "/") {
			return w, nil
		}
	}
	return "", ErrNoDefinition
}

// shellWords splits on blanks, honouring single quotes and backslash escapes (enough for shQuote output).
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inWord, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quoted && c == '\'':
			quoted = false
		case quoted:
			cur.WriteByte(c)
		case c == '\'':
			quoted, inWord = true, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

var commandRe = regexp.MustCompile(`<Command>([^<]*)</Command>`)

// InstalledBin reads <Command> from the registered task's XML.
func (w *schtasks) InstalledBin() (string, error) {
	out, err := w.s.exec("", "schtasks", "/Query", "/TN", TaskName, "/XML")
	if err != nil {
		return "", err
	}
	m := commandRe.FindStringSubmatch(strings.ReplaceAll(out, "\x00", ""))
	if m == nil {
		return "", ErrNoDefinition
	}
	return html.UnescapeString(strings.TrimSpace(m[1])), nil
}
