package picker

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func regionFixture() []RegionItem {
	return []RegionItem{
		{Region: "us-phoenix-1", Default: true},
		{Region: "uk-london-1"},
		{Region: "us-ashburn-1"},
		{Region: "eu-frankfurt-1"},
		{Region: "ap-tokyo-1"},
	}
}

func newRegionTestModel(w, h int) model {
	m := newRegionModel("prodcorp", regionFixture(), false)
	m.resize(w, h)
	return m
}

func TestRegionGoldenDefaultMarked(t *testing.T) {
	v := newRegionTestModel(100, 40).View()
	if !strings.Contains(v, "Select a region for prodcorp") {
		t.Errorf("no title:\n%s", v)
	}
	// Given order is kept: the default first, no rank sort.
	if lines := strings.Split(v, "\n"); !strings.HasPrefix(lines[3], ">● us-phoenix-1") || !strings.HasSuffix(lines[3], "default") {
		t.Errorf("default row %q", lines[3])
	}
	golden(t, "region", v)
}

func TestRegionGoldenFiltered(t *testing.T) {
	m := send(newRegionTestModel(100, 40), typed("fra")...)
	golden(t, "region-filtered", m.View())
	if !m.update(key(keyEnter)) || m.choice != "eu-frankfurt-1" {
		t.Fatalf("choice %q", m.choice)
	}
}

func TestRegionGoldenNoMatch(t *testing.T) {
	m := send(newRegionTestModel(100, 40), typed("zzz")...)
	golden(t, "region-nomatch", m.View())
	if m.update(key(keyEnter)) {
		t.Error("enter with no match finished")
	}
}

func TestRegionViewNeverOverflows(t *testing.T) {
	for w := 1; w <= 40; w++ {
		for h := 1; h <= 6; h++ {
			for _, l := range strings.Split(newRegionTestModel(w, h).View(), "\n") {
				if lw := ansi.StringWidth(l); lw > w {
					t.Errorf("w=%d h=%d: %d cols %q", w, h, lw, l)
				}
			}
		}
	}
}

func TestPickRegionEnterDefault(t *testing.T) {
	c := &fakeConsole{chunks: []string{"\r"}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	r, err := PickRegion("prodcorp", regionFixture(), os.Stdin)
	if err != nil || r != "us-phoenix-1" {
		t.Fatalf("PickRegion = %q, %v", r, err)
	}
	assertCleared(t, c, 9) // title, filter, header, 5 rows, footer
}

func TestPickRegionEscCancels(t *testing.T) {
	c := &fakeConsole{chunks: []string{"\x1b", ""}, w: 100, h: 40, panicAt: -1}
	fakeTerminal(t, c)
	if _, err := PickRegion("prodcorp", regionFixture(), os.Stdin); !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled", err)
	}
}

func TestPickRegionNoTTY(t *testing.T) {
	if _, err := PickRegion("p", regionFixture(), nil); !errors.Is(err, ErrNoTTY) {
		t.Fatalf("got %v", err)
	}
}
