package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/token"
)

type statusRow struct {
	Profile          string `json:"profile"`
	Region           string `json:"region"`
	Session          bool   `json:"session"`
	Expires          string `json:"expires"`
	RemainingSeconds int64  `json:"remaining_seconds"`
	Active           bool   `json:"active"`
}

func newStatus(d *deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show every profile's region and session expiry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ps, err := loadProfiles()
			if err != nil {
				return err
			}
			now := d.now()
			active := os.Getenv("OCI_CLI_PROFILE")
			rows := make([]statusRow, 0, len(ps))
			for _, p := range ps {
				r := statusRow{Profile: p.Name, Region: p.Region, Session: p.IsSession(), Active: p.Name == active}
				if p.IsSession() {
					info, err := token.Read(p.SecurityTokenFile, now)
					if errors.Is(err, token.ErrInsecurePerms) {
						fmt.Fprintf(d.stderr, "oci-assume: warning: %s: %v (fix: chmod 600 %s)\n", p.Name, err, p.SecurityTokenFile)
					}
					if rem := info.Remaining(now); err == nil && rem > 0 {
						r.Expires = info.Expiry.UTC().Format(time.RFC3339)
						r.RemainingSeconds = int64(rem / time.Second)
					}
				}
				rows = append(rows, r)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			reserved := reservedNames(cmd.Root())
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PROFILE\tREGION\tEXPIRES\tSTATE")
			for _, r := range rows {
				region := r.Region
				if region == "" {
					region = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Profile, region, rowExpires(r), rowState(r, reserved))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func rowExpires(r statusRow) string {
	if !r.Session || r.RemainingSeconds <= 0 {
		return "—"
	}
	return expiresText(time.Duration(r.RemainingSeconds) * time.Second)
}

// rowState: an expired session shows its fix command even when active.
func rowState(r statusRow, reserved map[string]bool) string {
	switch {
	case r.Session && r.RemainingSeconds <= 0:
		return "○ expired · run: " + assumeCmd(r.Profile, reserved)
	case r.Active:
		return "● active"
	case r.Session:
		return "○ valid"
	}
	return "api-key"
}

// wrapperWords are names the shell hooks pass straight through instead of eval'ing.
var wrapperWords = []string{"daemon", "doctor", "help", "completion", "__complete", "__completeNoDesc"}

func reservedNames(root *cobra.Command) map[string]bool {
	m := map[string]bool{}
	for _, w := range wrapperWords {
		m[w] = true
	}
	for _, c := range root.Commands() {
		m[c.Name()] = true
		for _, a := range c.Aliases {
			m[a] = true
		}
	}
	return m
}

// assumeCmd adds "--" when the profile name would be read as a subcommand or flag.
func assumeCmd(profile string, reserved map[string]bool) string {
	if reserved[profile] || strings.HasPrefix(profile, "-") {
		return "oci-assume -- " + profile
	}
	return "oci-assume " + profile
}
