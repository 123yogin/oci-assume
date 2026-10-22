package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/shell"
)

func newInit() *cobra.Command {
	return &cobra.Command{
		Use:       "init zsh|bash|fish|pwsh",
		Short:     "Print the shell hook to eval from your shell rc file",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"zsh", "bash", "fish", "pwsh"},
		RunE: func(cmd *cobra.Command, args []string) error {
			sh, err := shell.Parse(args[0])
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), shell.Init(sh))
			return nil
		},
	}
}
