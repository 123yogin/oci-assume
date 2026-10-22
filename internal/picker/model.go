package picker

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"

	"github.com/123yogin/oci-assume/internal/shell"
)

const (
	title       = "Select an OCI profile"
	placeholder = "type to filter"
	noMatches   = "no matching profiles"
	newLabel    = "+ New profile…"
	maxRows     = 10
	gutter      = 3 // cursor, active marker, space
	sep         = "   "
	minCol      = 4
	defaultW    = 80
	warnBelow   = 10 * time.Minute // matches the default refresh threshold
)

// ANSI-16 SGR codes, so colours follow the user's terminal theme.
const (
	sgrBold   = "1"
	sgrDim    = "2"
	sgrAccent = "1;35"
	sgrGreen  = "32"
	sgrAmber  = "33"
	sgrBlue   = "34"
	sgrCaret  = "7"
)

// footers are tried widest first; the filter hint goes before the keys do.
var footers = []string{
	"↑/↓ move · type to filter · enter select · esc cancel",
	"↑/↓ move · enter select · esc cancel",
	"↑/↓ enter esc",
}

type model struct {
	items   []Item   // in rank order
	text    []string // fuzzy source: "name region"
	matches []int    // indices into items, best first
	query   string
	cursor  int // index into matches
	offset  int // first visible match
	width   int
	height  int // 0 when unknown
	nameW   int
	regionW int
	statusW int
	color   bool

	choice     string
	cancelled  bool
	newProfile bool
	parked     bool // a query matched no profile: the cursor is off the new row until ↑/↓
}

func newModel(items []Item, color bool) model {
	sorted := append([]Item(nil), items...)
	Sort(sorted)
	m := model{items: sorted, color: color, nameW: len("PROFILE"), regionW: len("REGION"), statusW: len("SESSION")}
	for _, it := range sorted {
		if it.New {
			m.text = append(m.text, "") // never fuzzy-matched; filter keeps it last
			continue
		}
		m.text = append(m.text, it.Profile.Name+" "+region(it))
		m.nameW = max(m.nameW, ansi.StringWidth(it.Profile.Name))
		m.regionW = max(m.regionW, ansi.StringWidth(region(it)))
		m.statusW = max(m.statusW, ansi.StringWidth(status(it)))
	}
	m.filter()
	return m
}

func region(it Item) string {
	if it.Profile.Region == "" {
		return "-"
	}
	return it.Profile.Region
}

func status(it Item) string {
	if !it.Profile.IsSession() {
		return "api-key"
	}
	return shell.FormatRemaining(it.Remaining)
}

func (m *model) resize(w, h int) {
	m.width, m.height = w, h
	m.scroll()
}

// update applies one key and reports whether the picker is finished.
func (m *model) update(k keyEvent) bool {
	switch k.kind {
	case keyEsc, keyCtrlC:
		m.cancelled = true
		return true
	case keyEnter:
		if len(m.matches) == 0 || m.parked {
			return false
		}
		it := m.items[m.matches[m.cursor]]
		m.choice, m.newProfile = it.Profile.Name, it.New
		if it.New {
			m.choice = ""
		}
		return true
	case keyUp:
		m.move(-1)
	case keyDown:
		m.move(1)
	case keyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
			m.filter()
		}
	case keyRune:
		m.query += string(k.r)
		m.filter()
	}
	return false
}

func (m *model) move(d int) {
	if m.parked {
		m.parked = false
		return
	}
	if n := len(m.matches); n > 0 {
		m.cursor = (m.cursor + d + n) % n
		m.scroll()
	}
}

// filter keeps rank order for an empty query, else fuzzy score with rank as tiebreak.
// The new-profile row always stays, last.
func (m *model) filter() {
	m.cursor, m.offset, m.parked = 0, 0, false
	m.matches = m.matches[:0]
	if m.query == "" {
		for i := range m.items {
			m.matches = append(m.matches, i)
		}
		return
	}
	defer m.keepNew()
	res := fuzzy.FindNoSort(m.query, m.text)
	sort.SliceStable(res, func(i, j int) bool {
		if res[i].Score != res[j].Score {
			return res[i].Score > res[j].Score
		}
		return res[i].Index < res[j].Index
	})
	for _, r := range res {
		m.matches = append(m.matches, r.Index)
	}
}

func (m *model) keepNew() {
	none := len(m.matches) == 0
	for i, it := range m.items {
		if it.New {
			m.matches = append(m.matches, i)
			m.parked = none
		}
	}
}

// noProfileMatch reports a query that matched only the new row.
func (m model) noProfileMatch() bool {
	return m.query != "" && len(m.matches) > 0 && m.profiles(m.matches) == 0
}

// profiles counts the real profiles among idx.
func (m model) profiles(idx []int) int {
	n := 0
	for _, i := range idx {
		if !m.items[i].New {
			n++
		}
	}
	return n
}

// layout picks the chrome that fits: the header and footer go first, then the title.
// A one-line terminal shows the selected row, or the filter while typing.
func (m model) layout() (titled, filter, chrome bool, rows int) {
	rows = max(1, min(maxRows, len(m.items)))
	switch h := m.height; {
	case h <= 0:
		return true, true, true, rows
	case h >= 5:
		return true, true, true, max(1, min(rows, h-4))
	case h >= 3:
		return true, true, false, max(1, min(rows, h-2))
	case h == 2:
		return false, true, false, 1
	}
	if m.query == "" && len(m.matches) > 0 {
		return false, false, false, 1
	}
	return false, true, false, 0
}

func (m model) listHeight() int { _, _, _, n := m.layout(); return max(1, n) }

// scroll keeps the cursor inside the visible window.
func (m *model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, len(m.matches)-h))
}

// columns shrinks the name and region columns, widest first, to fit width w.
func (m model) columns(w int) (nameW, regionW int) {
	nameW, regionW = m.nameW, m.regionW
	avail := w - gutter - 2*len(sep) - m.statusW
	for nameW+regionW > avail {
		switch {
		case nameW >= regionW && nameW > minCol:
			nameW--
		case regionW > minCol:
			regionW--
		default:
			return nameW, regionW
		}
	}
	return nameW, regionW
}

func (m model) paint(sgr, s string) string {
	if !m.color || sgr == "" || s == "" {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

func pad(n int) string { return strings.Repeat(" ", max(0, n)) }

// cell truncates s to w columns with "…", paints it, and pads it to w.
func (m model) cell(sgr, s string, w int) string {
	s = ansi.Truncate(s, max(0, w), "…")
	return m.paint(sgr, s) + pad(w-ansi.StringWidth(s))
}

func statusSGR(it Item) string {
	switch {
	case !it.Profile.IsSession():
		return sgrBlue
	case it.Remaining <= 0:
		return sgrDim
	case it.Remaining < warnBelow:
		return sgrAmber
	}
	return sgrGreen
}

func (m model) row(i int, selected bool, nameW, regionW int) string {
	it := m.items[i]
	cur, mark, nameSGR := " ", " ", ""
	if selected {
		cur, nameSGR = m.paint(sgrAccent, ">"), sgrAccent
	}
	if it.New {
		return cur + mark + " " + m.paint(nameSGR, newLabel)
	}
	if it.Active {
		mark = m.paint(sgrGreen, "●")
	}
	return cur + mark + " " + m.cell(nameSGR, it.Profile.Name, nameW) + sep +
		m.cell("", region(it), regionW) + sep + m.paint(statusSGR(it), status(it))
}

// fitFooter picks the widest footer that fits in w columns.
func fitFooter(w int) string {
	for _, f := range footers {
		if ansi.StringWidth(f) <= w {
			return f
		}
	}
	return footers[len(footers)-1]
}

// View renders the frame; every line is clamped to the terminal width.
func (m model) View() string {
	w := m.width
	if w <= 0 {
		w = defaultW
	}
	titled, filter, chrome, rows := m.layout()
	nameW, regionW := m.columns(w)
	all := make([]int, len(m.items))
	for i := range all {
		all[i] = i
	}
	count := fmt.Sprintf("%d/%d", m.profiles(m.matches), m.profiles(all))
	foot := fitFooter(w)
	rowW := gutter + nameW + regionW + 2*len(sep) + m.statusW
	panelW := min(w, max(rowW, ansi.StringWidth(foot), len(title)+1+len(count)))

	var b []string
	if titled {
		t := ansi.Truncate(title, max(0, panelW-len(count)-1), "…")
		b = append(b, m.paint(sgrBold, t)+pad(panelW-ansi.StringWidth(t)-len(count))+m.paint(sgrDim, count))
	}
	switch {
	case !filter:
	case m.query == "":
		b = append(b, m.paint(sgrAccent, ">")+" "+m.paint(sgrDim, placeholder))
	default:
		b = append(b, m.paint(sgrAccent, ">")+" "+m.query+m.paint(sgrCaret, " "))
	}
	if chrome {
		b = append(b, m.paint(sgrDim, pad(gutter)+m.cell("", "PROFILE", nameW)+sep+
			m.cell("", "REGION", regionW)+sep+"SESSION"))
	}
	shift := 0
	if m.noProfileMatch() {
		shift = 1 // "no matching profiles" sits above the new row
	}
	for i := 0; i < rows; i++ {
		j := m.offset + i - shift
		switch {
		case (len(m.matches) == 0 || shift == 1) && i == 0:
			b = append(b, pad(gutter)+m.paint(sgrDim, noMatches))
		case j < len(m.matches):
			b = append(b, m.row(m.matches[j], j == m.cursor && !m.parked, nameW, regionW))
		default:
			b = append(b, "")
		}
	}
	if chrome {
		b = append(b, m.paint(sgrDim, foot))
	}
	for i, l := range b {
		b[i] = ansi.Truncate(l, w, "…")
	}
	return strings.Join(b, "\n")
}
