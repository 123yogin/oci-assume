package shell

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/123yogin/oci-assume/internal/testutil/gobuild"
)

// stubPath is a PATH entry holding the Go-built oci-assume stand-in, so pwsh on Windows can run it.
var stubPath string

func TestMain(m *testing.M) {
	bin, err := gobuild.Build("github.com/123yogin/oci-assume/internal/testutil/wrapstub", "oci-assume")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stubPath = filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
	code := m.Run()
	gobuild.Cleanup()
	os.Exit(code)
}

func TestParse(t *testing.T) {
	for _, s := range []string{"zsh", "bash", "fish", "pwsh"} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q): %v", s, err)
		}
	}
	if _, err := Parse("tcsh"); err == nil {
		t.Error("want error for tcsh")
	}
}

func TestExportZsh(t *testing.T) {
	got := Export(Zsh, Env{"dev", "us-ashburn-1", true})
	want := "export OCI_CLI_PROFILE='dev'\nexport OCI_CLI_AUTH='security_token'\nexport OCI_CLI_REGION='us-ashburn-1'\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestExportFish(t *testing.T) {
	got := Export(Fish, Env{"dev", "r", true})
	if !strings.HasPrefix(got, "set -gx OCI_CLI_PROFILE 'dev'\n") {
		t.Fatalf("got %q", got)
	}
}

func TestExportPwsh(t *testing.T) {
	got := Export(Pwsh, Env{"dev", "r", true})
	if !strings.HasPrefix(got, "$env:OCI_CLI_PROFILE = 'dev'\n") {
		t.Fatalf("got %q", got)
	}
}

func TestFishUnsetLineIsStatusNeutral(t *testing.T) {
	if got := unsetLine(Fish, "X"); got != "set -e X; or true\n" {
		t.Fatalf("got %q", got)
	}
}

func TestExportClearsAuthAndRegionForAPIKey(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Fish, Pwsh} {
		got := Export(sh, Env{Profile: "dev"})
		for _, k := range []string{"OCI_CLI_AUTH", "OCI_CLI_REGION"} {
			if !strings.Contains(got, unsetLine(sh, k)) || strings.Contains(got, setLine(sh, k, "")) {
				t.Errorf("%s %s: got %q", sh, k, got)
			}
		}
	}
}

func shellBin(t *testing.T, sh Shell) string {
	t.Helper()
	p, err := exec.LookPath(string(sh))
	if err != nil {
		t.Skipf("%s not installed", sh)
	}
	return p
}

// runIn runs script in sh and returns stdout.
// POSIX shells read it from a file: MSYS bash on Windows mangles backslashes in -c arguments.
func runIn(t *testing.T, sh Shell, script string, env ...string) string {
	t.Helper()
	bin := shellBin(t, sh)
	var args []string
	if sh == Pwsh {
		args = []string{"-NoProfile", "-NonInteractive", "-Command", script}
	} else {
		f := filepath.Join(t.TempDir(), "script")
		if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		args = []string{filepath.ToSlash(f)}
		if sh == Fish {
			args = []string{"--no-config", filepath.ToSlash(f)}
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", sh, err, out)
	}
	return string(out)
}

func TestExportQuotesHostileNames(t *testing.T) {
	names := []string{`it's`, `$x`, "`id`", `\\`, `%F{red}`, `\'`, "\u2019", "\u2018", "x\u2019; Write-Output PWNED; \u2019", `it's $x "y"`}
	for _, sh := range []Shell{Zsh, Bash, Fish, Pwsh} {
		for _, name := range names {
			t.Run(string(sh), func(t *testing.T) {
				shellBin(t, sh)
				print := `printf '%s' "$OCI_CLI_PROFILE"`
				switch sh {
				case Fish:
					print = `printf '%s' $OCI_CLI_PROFILE`
				case Pwsh:
					print = `[Console]::Out.Write($env:OCI_CLI_PROFILE)`
				}
				got := runIn(t, sh, Export(sh, Env{Profile: name})+print)
				if got != name {
					t.Fatalf("got %q want %q", got, name)
				}
			})
		}
	}
}

func TestValidate(t *testing.T) {
	for _, e := range []Env{{Profile: "dev", Region: "us-ashburn-1"}, {Profile: "it's $x"}, {Profile: "a\u2019b"}} {
		if err := Validate(e); err != nil {
			t.Errorf("%+v: %v", e, err)
		}
	}
	for _, e := range []Env{{Profile: "a\nb"}, {Profile: "a\x00b"}, {Profile: "a\x7fb"}, {Profile: "a\x1bb"}, {Profile: "d", Region: "US-1"}, {Profile: "d", Region: "x;y"}, {Profile: "d", Region: "a b"}} {
		if Validate(e) == nil {
			t.Errorf("%+v: want error", e)
		}
	}
}

func TestUnset(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Fish, Pwsh} {
		t.Run(string(sh), func(t *testing.T) {
			shellBin(t, sh)
			print := `printf '[%s]' "$OCI_CLI_PROFILE"`
			switch sh {
			case Fish:
				print = `printf '[%s]' $OCI_CLI_PROFILE`
			case Pwsh:
				print = `[Console]::Out.Write('[' + $env:OCI_CLI_PROFILE + ']')`
			}
			got := runIn(t, sh, Unset(sh)+print, "OCI_CLI_PROFILE=x")
			if got != "[]" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	m := time.Minute
	cases := []struct {
		rem, warn time.Duration
		text      string
		st        PromptState
	}{
		{52 * m, 10 * m, "dev 52m", StateOK},
		{9 * m, 10 * m, "dev 9m", StateWarn},
		{0, 10 * m, "dev expired", StateExpired},
		{-m, 10 * m, "dev expired", StateExpired},
		{125 * m, 10 * m, "dev 2h05m", StateOK},
		{59*m + 59*time.Second, 10 * m, "dev 59m", StateOK},
		{60 * m, 10 * m, "dev 1h00m", StateOK},
		{0, -1, "dev", StateOK},
	}
	for _, c := range cases {
		txt, st := Prompt("dev", c.rem, c.warn)
		if txt != c.text || st != c.st {
			t.Errorf("Prompt(%v,%v) = %q,%q want %q,%q", c.rem, c.warn, txt, st, c.text, c.st)
		}
	}
	if txt, st := Prompt("", time.Hour, m); txt != "" || st != "" {
		t.Errorf("empty profile: %q %q", txt, st)
	}
}

func TestFormatRemaining(t *testing.T) {
	m := time.Minute
	cases := map[time.Duration]string{
		52 * m: "52m", 59*m + 59*time.Second: "59m", 60 * m: "1h00m",
		125 * m: "2h05m", 0: "expired", -m: "expired",
	}
	for d, want := range cases {
		if got := FormatRemaining(d); got != want {
			t.Errorf("FormatRemaining(%v) = %q want %q", d, got, want)
		}
	}
}

func TestInitContainsNoUserData(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Fish, Pwsh} {
		t.Setenv("HOME", "/home/a")
		a := Init(sh)
		t.Setenv("HOME", "/home/b")
		if b := Init(sh); a != b || a == "" {
			t.Errorf("%s: init differs or is empty", sh)
		}
		prompt := "oci_assume_prompt"
		if sh == Pwsh {
			prompt = "OciAssumePrompt"
		}
		if !strings.Contains(a, "OCI_ASSUME_SHELL") || !strings.Contains(a, prompt) {
			t.Errorf("%s: missing markers", sh)
		}
	}
	if Init("tcsh") != "" {
		t.Error("unknown shell should give empty script")
	}
}

func TestWrapperEvaluatesOnSuccess(t *testing.T) {
	cases := []struct {
		sh    Shell
		line  string
		check string
	}{
		{Zsh, `export OCI_CLI_PROFILE='dev'`, `printf '%s|%s' "$OCI_CLI_PROFILE" "$OCI_ASSUME_SHELL"`},
		{Bash, `export OCI_CLI_PROFILE='dev'`, `printf '%s|%s' "$OCI_CLI_PROFILE" "$OCI_ASSUME_SHELL"`},
		{Pwsh, `$env:OCI_CLI_PROFILE = 'dev'`, `[Console]::Out.Write($env:OCI_CLI_PROFILE + '|' + $env:OCI_ASSUME_SHELL)`},
		{Fish, `set -gx OCI_CLI_PROFILE 'dev'`, `printf '%s|%s' $OCI_CLI_PROFILE $OCI_ASSUME_SHELL`},
	}
	for _, c := range cases {
		t.Run(string(c.sh), func(t *testing.T) {
			shellBin(t, c.sh)
			init := Init(c.sh)
			got := runIn(t, c.sh, init+"\noci-assume dev\n"+c.check, "PATH="+stubPath, "STUB_OUT="+c.line, "OCI_CLI_PROFILE=")
			if got != "dev|"+string(c.sh) {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestWrapperSkipsEvalOnFailureAndPassthrough(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Pwsh, Fish} {
		t.Run(string(sh), func(t *testing.T) {
			shellBin(t, sh)
			var set string
			switch sh {
			case Fish:
				set = "set -gx OCI_CLI_PROFILE 'bad'"
			case Pwsh:
				set = "$env:OCI_CLI_PROFILE = 'bad'"
			default:
				set = "export OCI_CLI_PROFILE='bad'"
			}
			// Fails on "dev"; for "status" prints the line as data and must not eval it.
			var script string
			switch sh {
			case Fish:
				script = "oci-assume dev >/dev/null; set rc $status; oci-assume status >/dev/null; printf '%s|[%s]' $rc \"$OCI_CLI_PROFILE\""
			case Pwsh:
				script = "oci-assume dev | Out-Null; $rc = $LASTEXITCODE; oci-assume status | Out-Null; [Console]::Out.Write(\"$rc|[$env:OCI_CLI_PROFILE]\")"
			default:
				script = `oci-assume dev >/dev/null; rc=$?; oci-assume status >/dev/null; printf '%s|[%s]' "$rc" "$OCI_CLI_PROFILE"`
			}
			got := runIn(t, sh, Init(sh)+"\n"+script, "PATH="+stubPath, "STUB_OUT="+set, "STUB_EXIT=2", "OCI_CLI_PROFILE=")
			if got != "2|[]" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestWrapperSetsEvalEnvOnlyOnEvalPath(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Pwsh, Fish} {
		t.Run(string(sh), func(t *testing.T) {
			shellBin(t, sh)
			log := filepath.Join(t.TempDir(), "log")
			script := map[Shell]string{
				Zsh: "oci-assume dev; oci-assume status", Bash: "oci-assume dev; oci-assume status",
				Fish: "oci-assume dev; oci-assume status", Pwsh: "oci-assume dev; oci-assume status; [Console]::Out.Write('after=' + [string]$env:OCI_ASSUME_EVAL)",
			}[sh]
			out := runIn(t, sh, Init(sh)+"\n"+script, "PATH="+stubPath, "STUB_LOG="+log, "OCI_ASSUME_EVAL=")
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != "dev=1\nstatus=unset\n" && string(b) != "dev=1\nstatus=\n" {
				t.Fatalf("log %q out %q", b, out)
			}
			if sh == Pwsh && !strings.Contains(out, "after=") || strings.Contains(out, "after=1") {
				t.Fatalf("eval env leaked: %q", out)
			}
		})
	}
}

// Cobra completion scripts call the function with __complete; it must bypass eval (R12).
func TestWrapperPassesCompletionThrough(t *testing.T) {
	for _, sh := range []Shell{Zsh, Bash, Pwsh, Fish} {
		t.Run(string(sh), func(t *testing.T) {
			shellBin(t, sh)
			log := filepath.Join(t.TempDir(), "log")
			script := `oci-assume __complete ""; oci-assume __completeNoDesc ""`
			out := runIn(t, sh, Init(sh)+"\n"+script, "PATH="+stubPath, "STUB_LOG="+log, "STUB_OUT=cand", "OCI_ASSUME_EVAL=")
			b, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if s := string(b); s != "__complete=unset\n__completeNoDesc=unset\n" {
				t.Fatalf("log %q", s)
			}
			if strings.Count(out, "cand") != 2 {
				t.Fatalf("candidates lost: %q", out)
			}
		})
	}
}
