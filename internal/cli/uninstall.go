package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	onboard "github.com/123yogin/oci-assume/internal/setup"
)

// removalHints lists package-manager commands; they are printed, never run.
const removalHints = `To remove the oci-assume binary, use the tool that installed it:
  brew uninstall --cask oci-assume
  scoop uninstall oci-assume
  winget uninstall oci-assume
  sudo apt remove oci-assume   (or: sudo dnf remove oci-assume)
  rm "$(command -v oci-assume)"   (install.sh or go install)
`

func newUninstall(d *deps) *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Undo everything setup changed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := setupEnv(cmd, d, "")
			if err != nil {
				return err
			}
			if err := onboard.Uninstall(e, d.setupSteps(e), purge); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Removed what oci-assume setup added.")
			fmt.Fprint(out, removalHints)
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the state directory and logs")
	return cmd
}
