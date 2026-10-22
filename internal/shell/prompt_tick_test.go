package shell

import (
	"os"
	"path/filepath"
	"testing"
)

// The prompt helpers pass --tick and print exactly what they printed before.
func TestPromptHelperTickSameOutput(t *testing.T) {
	call := map[Shell]string{Zsh: "oci_assume_prompt", Bash: "oci_assume_prompt", Fish: "oci_assume_prompt",
		Pwsh: "[Console]::Out.Write((OciAssumePrompt))"}
	for _, sh := range []Shell{Zsh, Bash, Pwsh, Fish} {
		t.Run(string(sh), func(t *testing.T) {
			shellBin(t, sh)
			argv := filepath.Join(t.TempDir(), "argv")
			out := runIn(t, sh, Init(sh)+"\n"+call[sh], "PATH="+stubPath, "STUB_ARGV="+argv, "STUB_OUT=dev 30m")
			if out != "[oci dev 30m]" {
				t.Fatalf("output %q", out)
			}
			b, err := os.ReadFile(argv)
			if err != nil {
				t.Fatal(err)
			}
			if s := string(b); s != "prompt --tick\n" {
				t.Fatalf("argv %q", s)
			}
		})
	}
}
