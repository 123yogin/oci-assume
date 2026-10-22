package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func versionString() string {
	return fmt.Sprintf("oci-assume %s (%s, %s)", Version, Commit, Date)
}

func newVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	}
}
