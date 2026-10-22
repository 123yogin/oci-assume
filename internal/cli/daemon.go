package cli

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/state"
)

const scheduledCmd = "refresh --all --background"

func newDaemon(d *deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon install|uninstall|status",
		Short: "Manage the background job that refreshes recent sessions every 30 min",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Install (or reinstall) the scheduler entry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := d.scheduler(d.goos)
			if err != nil {
				return err
			}
			bin, err := d.binPath()
			if err != nil {
				return err
			}
			if err := b.Install(bin); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if st, err := b.Status(); err == nil {
				fmt.Fprintf(out, "installed: %s\n", st.Path)
			}
			fmt.Fprintf(out, "runs every 30 min: %s %s\n", bin, scheduledCmd)
			return nil
		},
	}, &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the scheduler entry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := d.scheduler(d.goos)
			if err != nil {
				return err
			}
			if err := b.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "uninstalled")
			return nil
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Show whether the job is installed and the last result per profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := d.scheduler(d.goos)
			if err != nil {
				return err
			}
			st, err := b.Status()
			if err != nil {
				return err
			}
			s, err := state.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "installed: %s\npath: %s\n", yesNo(st.Installed), st.Path)
			if st.LastRun.IsZero() {
				fmt.Fprintln(out, "last refresh: never")
				return nil
			}
			fmt.Fprintf(out, "last refresh: %s (%s ago)\n", st.LastRun.UTC().Format(time.RFC3339), ago(d.now().Sub(st.LastRun)))
			names := make([]string, 0, len(s.LastResult))
			for n, r := range s.LastResult {
				if r.Background {
					names = append(names, n)
				}
			}
			sort.Strings(names)
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			for _, n := range names {
				r := s.LastResult[n]
				res := "ok"
				if !r.OK {
					res = "failed"
				}
				line := fmt.Sprintf("  %s\t%s\t%s", n, res, r.At.UTC().Format(time.RFC3339))
				if r.Msg != "" {
					line += "\t" + r.Msg
				}
				fmt.Fprintln(tw, line)
			}
			return tw.Flush()
		},
	})
	return cmd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ago renders a duration coarsely: 45s, 12m, 3h, 2d.
func ago(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
