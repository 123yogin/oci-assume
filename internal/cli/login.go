package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/ensure"
)

func newLogin(d *deps, f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "login <profile>",
		Short: "Force a browser login (oci session authenticate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, loginTimeout, err := f.durations()
			if err != nil {
				return err
			}
			ps, err := loadProfiles()
			if err != nil {
				return err
			}
			p, err := findProfile(ps, args[0])
			if err != nil {
				return err
			}
			region := p.Region
			if f.region != "" {
				region = f.region
			}
			r, err := d.newRunner()
			if err != nil {
				return err
			}
			if err := ensure.Login(cmd.Context(), r, p, region, loginTimeout); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: logged in\n", p.Name)
			return nil
		},
	}
}
