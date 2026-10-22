package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/shell"
)

type promptJSON struct {
	Profile          string `json:"profile"`
	Text             string `json:"text"`
	State            string `json:"state"`
	RemainingSeconds int64  `json:"remaining_seconds"`
}

// newPrompt never errors and never builds an oci runner: it runs on every shell prompt.
func newPrompt(d *deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Print the active profile and session time left, for shell prompts",
		Args:  cobra.ArbitraryArgs,
		// A bad flag from a prompt config must not error either.
		FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := os.Getenv("OCI_CLI_PROFILE")
			if name == "" {
				return nil
			}
			ps, err := ociconfig.Load(ociconfig.DefaultPath())
			if err != nil {
				return nil
			}
			p, ok := ociconfig.Find(ps, name)
			if !ok {
				return nil
			}
			warn, err := resolveThreshold("")
			if err != nil {
				warn = 10 * time.Minute
			}
			rem := remaining(p, d.now())
			if !p.IsSession() {
				warn, rem = -1, 0
			}
			text, st := shell.Prompt(p.Name, rem, warn)
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(promptJSON{p.Name, text, string(st), int64(rem / time.Second)})
			}
			fmt.Fprintln(out, text)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}
