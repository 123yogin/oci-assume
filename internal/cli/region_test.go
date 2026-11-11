package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/picker"
	"github.com/123yogin/oci-assume/internal/regions"
)

func regionNames(items []picker.RegionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Region
	}
	return out
}

func TestPickerThenRegionStepExportsOverride(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty, e.pickName, e.regionName = true, "dev", "eu-frankfurt-1"
	t.Setenv("OCI_CLI_REGION", "zz-local-1")
	if code := e.eval(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='eu-frankfurt-1'") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
	if e.regionFor != "dev" {
		t.Fatalf("region step for %q", e.regionFor)
	}
	// The profile's own region first and marked, then other config regions, then $OCI_CLI_REGION, then the built-ins.
	got := regionNames(e.regionItems)
	want := regions.Merge([]string{"us-phoenix-1", "us-ashburn-1", "zz-local-1"}, regions.Known)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows %v", got)
	}
	for i, it := range e.regionItems {
		if it.Default != (i == 0) {
			t.Fatalf("row %d default=%v", i, it.Default)
		}
	}
}

func TestRegionStepDefaultRowKeepsProfileRegion(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty, e.pickName = true, "dev"
	if code := e.eval(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if e.regionItems == nil || !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='us-phoenix-1'") {
		t.Fatalf("ran %v stdout %q", e.regionItems != nil, e.stdout.String())
	}
}

// An override is only exported; login keeps the config region so oci never rewrites region=.
func TestOverrideNeverReachesAuthenticate(t *testing.T) {
	for name, args := range map[string][]string{"region step": nil, "-r": {"-r", "eu-frankfurt-1", "dev"}} {
		t.Run(name, func(t *testing.T) {
			e := setup(t, "ok")
			e.token(-time.Minute)
			e.tty, e.pickName, e.regionName = true, "dev", "eu-frankfurt-1"
			if code := e.eval(args...); code != 0 {
				t.Fatalf("exit %d: %s", code, e.stderr.String())
			}
			calls := e.calls()
			if !strings.Contains(calls, "session authenticate") || !strings.Contains(calls, "--region us-phoenix-1") || strings.Contains(calls, "eu-frankfurt-1") {
				t.Fatalf("oci calls %q", calls)
			}
			if !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='eu-frankfurt-1'") {
				t.Fatalf("stdout %q", e.stdout.String())
			}
		})
	}
}

// writeConfig replaces the test config with body.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(os.Getenv("OCI_CLI_CONFIG_FILE"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRegionStepNoneRowForRegionlessProfile(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	writeConfig(t, "[norgn]\nsecurity_token_file="+e.tokPath+"\n[other]\nregion=us-ashburn-1\nkey_file=/k\n")
	e.tty, e.pickName = true, "norgn"
	if code := e.eval(); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if len(e.regionItems) < 2 || e.regionItems[0] != (picker.RegionItem{Region: "", Default: true}) || e.regionItems[1].Region != "us-ashburn-1" {
		t.Fatalf("rows %v", e.regionItems[:min(3, len(e.regionItems))])
	}
	if !strings.Contains(e.stdout.String(), "unset OCI_CLI_REGION") {
		t.Fatalf("stdout %q", e.stdout.String())
	}
}

func TestRegionStepSkippedForInvalidConfigRegion(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	writeConfig(t, "[bad]\nregion=Bad Region\nsecurity_token_file="+e.tokPath+"\n")
	e.tty, e.pickName = true, "bad"
	if code := e.eval(); code != 4 || e.stdout.Len() != 0 || e.regionItems != nil {
		t.Fatalf("exit %d stdout %q step ran %v", code, e.stdout.String(), e.regionItems != nil)
	}
}

func TestRegionStepSkippedAfterNewProfile(t *testing.T) {
	for name, args := range map[string][]string{"bare": nil, "-R": {"-R"}} {
		t.Run(name, func(t *testing.T) {
			e := setup(t, "ok")
			e.token(time.Hour)
			e.tty = true
			pick := func([]picker.Item, *os.File) (string, error) { return "", picker.ErrNewProfile }
			code, _ := runLogin(t, e, os.Getenv("OCI_CLI_CONFIG_FILE"), "newp\n\n", pick, args...)
			if code != 0 || e.regionItems != nil {
				t.Fatalf("exit %d step ran %v stderr %q", code, e.regionItems != nil, e.stderr.String())
			}
		})
	}
}

func TestProfileCompletion(t *testing.T) {
	e := setup(t, "ok")
	if code := e.run("__complete", "-R", "d"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if out := e.stdout.String(); !strings.HasPrefix(out, "dev\n") || strings.Contains(out, "prod-admin") {
		t.Fatalf("completion %q", out)
	}
	// Bare: subcommands and profile names together.
	e.run("__complete", "d")
	if out := e.stdout.String(); !strings.Contains(out, "dev\n") || !strings.Contains(out, "daemon") {
		t.Fatalf("bare completion %q", out)
	}
}

func TestProfileCompletionSkipsUnusableNames(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	body := "[good]\nregion=us-ashburn-1\n[g\tab]\n[-rf]\n[good two]\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCI_CLI_CONFIG_FILE", cfg)
	got, _ := completeProfile(nil, nil, "")
	want := []string{"good", "good two"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("completion %q, want %q", got, want)
	}
}

func TestRegionStepSkipped(t *testing.T) {
	cases := map[string]struct {
		tty  bool
		args []string
		want string
	}{
		"named profile": {true, []string{"dev"}, "us-phoenix-1"},
		"-r":            {true, []string{"-r", "us-ashburn-1"}, "us-ashburn-1"},
		"no tty":        {false, []string{"dev"}, "us-phoenix-1"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t, "ok")
			e.token(time.Hour)
			e.tty, e.pickName = c.tty, "dev"
			if code := e.eval(c.args...); code != 0 {
				t.Fatalf("exit %d: %s", code, e.stderr.String())
			}
			if e.regionItems != nil {
				t.Fatal("region step ran")
			}
			if !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='"+c.want+"'") {
				t.Fatalf("stdout %q", e.stdout.String())
			}
		})
	}
}

func TestPickRegionFlagForcesStep(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty, e.regionName = true, "ap-tokyo-1"
	if code := e.eval("-R", "dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	if e.regionFor != "dev" || !strings.Contains(e.stdout.String(), "OCI_CLI_REGION='ap-tokyo-1'") {
		t.Fatalf("for %q stdout %q", e.regionFor, e.stdout.String())
	}
}

func TestPickRegionFlagNoTTYExits4(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	if code := e.eval("--pick-region", "dev"); code != 4 || e.stdout.Len() != 0 || e.regionItems != nil {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
}

func TestPickRegionFlagWithRegionExits4(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty = true
	if code := e.eval("-R", "-r", "us-ashburn-1", "dev"); code != 4 || e.stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
}

func TestRegionStepEscExits1(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	e.tty, e.pickName, e.regionErr = true, "dev", picker.ErrCancelled
	if code := e.eval(); code != 1 || e.stdout.Len() != 0 {
		t.Fatalf("exit %d stdout %q", code, e.stdout.String())
	}
	if e.runners != 0 {
		t.Fatal("cancelled assume built a runner")
	}
}

func TestRegionFlagCompletion(t *testing.T) {
	e := setup(t, "ok")
	t.Setenv("OCI_CLI_REGION", "us-zz-1")
	if code := e.run("__complete", "-r", "us-"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.stderr.String())
	}
	want := "us-phoenix-1\nus-ashburn-1\nus-zz-1\nus-chicago-1\nus-sanjose-1\n:4\n"
	if got := e.stdout.String(); !strings.HasPrefix(got, want) {
		t.Fatalf("completion:\n%s\nwant prefix:\n%s", got, want)
	}
	e.run("__complete", "--region", "")
	if lines := strings.Split(e.stdout.String(), "\n"); lines[0] != "us-phoenix-1" || len(lines) < len(regions.Known) {
		t.Fatalf("empty prefix: %q", e.stdout.String())
	}
}

func TestPromptRegionOverride(t *testing.T) {
	e := setup(t, "ok")
	e.token(52*time.Minute + 30*time.Second)
	t.Setenv("OCI_ASSUME_OCI_BIN", "/bin/false")
	t.Setenv("OCI_CLI_PROFILE", "dev")
	for region, want := range map[string]string{"eu-frankfurt-1": "dev eu-frankfurt-1 52m", "us-phoenix-1": "dev 52m", "": "dev 52m"} {
		t.Setenv("OCI_CLI_REGION", region)
		if code := e.run("prompt"); code != 0 || strings.TrimSpace(e.stdout.String()) != want {
			t.Errorf("%q: exit %d stdout %q, want %q", region, code, e.stdout.String(), want)
		}
	}
	t.Setenv("OCI_CLI_REGION", "eu-frankfurt-1")
	e.run("prompt", "--json")
	var m map[string]any
	if err := json.Unmarshal(e.stdout.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["region"] != "eu-frankfurt-1" || m["text"] != "dev eu-frankfurt-1 52m" {
		t.Fatalf("json %v", m)
	}
	t.Setenv("OCI_CLI_REGION", "")
	e.run("prompt", "--json")
	if err := json.Unmarshal(e.stdout.Bytes(), &m); err != nil || m["region"] != "us-phoenix-1" {
		t.Fatalf("json %v %v", m, err)
	}
	if e.calls() != "" || e.runners != 0 {
		t.Fatalf("prompt touched oci")
	}
}

func TestStatusRegionOverrideMarker(t *testing.T) {
	e := setup(t, "ok")
	e.token(time.Hour)
	t.Setenv("OCI_CLI_PROFILE", "dev")
	t.Setenv("OCI_CLI_REGION", "eu-frankfurt-1")
	if code := e.run("status"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, l := range strings.Split(e.stdout.String(), "\n") {
		switch {
		case strings.HasPrefix(l, "dev "):
			if !strings.Contains(l, "eu-frankfurt-1 (override)") {
				t.Errorf("dev row %q", l)
			}
		case strings.Contains(l, "(override)"):
			t.Errorf("non-active row marked: %q", l)
		}
	}
	e.run("status", "--json")
	var rows []map[string]any
	if err := json.Unmarshal(e.stdout.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0]["region"] != "us-phoenix-1" || rows[0]["region_override"] != "eu-frankfurt-1" {
		t.Fatalf("dev json %v", rows[0])
	}
	if _, ok := rows[1]["region_override"]; ok {
		t.Fatalf("prod-admin json %v", rows[1])
	}
	t.Setenv("OCI_CLI_REGION", "us-phoenix-1")
	e.run("status")
	if strings.Contains(e.stdout.String(), "(override)") {
		t.Fatalf("same region marked:\n%s", e.stdout.String())
	}
}
