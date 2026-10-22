package cli

import (
	"runtime/debug"
	"testing"
)

func TestVersionFromBuildInfo(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	read := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true
	}
	cases := []struct{ in, want string }{
		{"dev", "v1.2.3"},
		{"1.0.0", "1.0.0"},
	}
	for _, c := range cases {
		Version = c.in
		if got := resolveVersion(read); got != c.want {
			t.Errorf("Version=%q: got %q, want %q", c.in, got, c.want)
		}
	}
	Version = "dev"
	devel := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true
	}
	if got := resolveVersion(devel); got != "dev" {
		t.Errorf("(devel): got %q", got)
	}
	if got := resolveVersion(func() (*debug.BuildInfo, bool) { return nil, false }); got != "dev" {
		t.Errorf("no build info: got %q", got)
	}
}
