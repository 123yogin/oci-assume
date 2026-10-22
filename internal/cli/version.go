package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// resolveVersion falls back to the module version for `go install @vX` builds.
func resolveVersion(read func() (*debug.BuildInfo, bool)) string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := read(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

func versionString() string {
	return fmt.Sprintf("oci-assume %s (%s, %s)", resolveVersion(debug.ReadBuildInfo), Commit, Date)
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
