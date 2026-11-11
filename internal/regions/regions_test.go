package regions

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestKnownIsSortedUniqueAndWellFormed(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]$`)
	if !slices.IsSorted(Known) {
		t.Error("Known is not sorted")
	}
	if len(slices.Compact(slices.Clone(Known))) != len(Known) {
		t.Error("Known has duplicates")
	}
	for _, r := range Known {
		if !re.MatchString(r) {
			t.Errorf("malformed region %q", r)
		}
	}
	for _, r := range []string{"us-ashburn-1", "us-phoenix-1", "eu-frankfurt-1", "ap-tokyo-1"} {
		if !slices.Contains(Known, r) {
			t.Errorf("missing %s", r)
		}
	}
}

func TestMergeKeepsFirstSeenOrderAndSkipsEmpty(t *testing.T) {
	got := Merge([]string{"us-phoenix-1"}, []string{"", "zz-local-1", "us-phoenix-1"}, []string{"us-ashburn-1"}, []string{"aa-x-1", "zz-local-1"})
	want := "us-phoenix-1,zz-local-1,us-ashburn-1,aa-x-1"
	if s := strings.Join(got, ","); s != want {
		t.Errorf("Merge = %s, want %s", s, want)
	}
}
