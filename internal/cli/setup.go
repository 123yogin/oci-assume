package cli

import (
	"bufio"
	"context"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	onboard "github.com/123yogin/oci-assume/internal/setup"
	"github.com/123yogin/oci-assume/internal/shell"
	"github.com/123yogin/oci-assume/internal/state"
)

// setupEnv builds the onboarding Env; output goes through cmd.OutOrStdout() (stderr under eval).
func setupEnv(cmd *cobra.Command, d *deps, sh string) (*onboard.Env, error) {
	dir, err := state.Dir()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, cfgErrorf("cannot determine home directory: %v", err)
	}
	return &onboard.Env{
		GOOS: d.goos, Home: home, StateDir: dir, Shell: sh,
		In: bufio.NewReader(cmd.InOrStdin()), Out: cmd.OutOrStdout(), // one reader shared by Run and prompting steps
		Interactive: d.isTTY() && os.Getenv("CI") == "",
		Exec: func(ctx context.Context, name string, args ...string) (string, error) {
			b, err := exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- steps pass fixed commands
			return string(b), err
		},
		ExecTTY: func(ctx context.Context, name string, args ...string) error {
			c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- steps pass fixed commands
			c.Stdin, c.Stdout, c.Stderr = d.stdin, d.stderr, d.stderr
			return c.Run()
		},
		LookPath:     exec.LookPath,
		Executable:   os.Executable,
		NewShellPath: onboard.NewShellPath,
		Getenv:       os.Getenv,
		Now:          d.now,
		Ctx:          cmd.Context(),
	}, nil
}

func newSetup(d *deps) *cobra.Command {
	var o onboard.Options
	var sh string
	var noShell, noBackground, noOCICLI, noLogin bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install the OCI CLI, shell hook and background refresh, then log in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if sh != "" {
				if _, err := shell.Parse(sh); err != nil {
					return cfgErrorf("invalid --shell %q (want zsh, bash, fish or pwsh)", sh)
				}
			}
			e, err := setupEnv(cmd, d, sh)
			if err != nil {
				return err
			}
			o.Skip = map[string]bool{"shell-hook": noShell, "background": noBackground, "oci-cli": noOCICLI, "first-login": noLogin}
			_, err = onboard.Run(e, d.setupSteps(e), o)
			return err
		},
	}
	fl := cmd.Flags()
	fl.BoolVarP(&o.Yes, "yes", "y", false, "accept every step without prompting")
	fl.BoolVar(&o.DryRun, "dry-run", false, "print the plan without changing anything")
	fl.BoolVar(&noShell, "no-shell", false, "skip the shell hook")
	fl.BoolVar(&noBackground, "no-background", false, "skip background refresh")
	fl.BoolVar(&noOCICLI, "no-oci-cli", false, "skip installing the OCI CLI")
	fl.BoolVar(&noLogin, "no-login", false, "skip the first login")
	fl.StringVar(&sh, "shell", "", "shell to set up: zsh|bash|fish|pwsh (default: detected)")
	return cmd
}
