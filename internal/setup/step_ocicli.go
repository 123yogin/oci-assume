package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ociInstallTimeout bounds a package-manager install of the OCI CLI.
const ociInstallTimeout = 15 * time.Minute

const (
	ociDocsURL      = "https://docs.oracle.com/iaas/Content/API/SDKDocs/cliinstall.htm"
	ociMSIURL       = "https://github.com/oracle/oci-cli/releases/latest"
	ociScriptCmd    = `bash -c "$(curl -L https://raw.githubusercontent.com/oracle/oci-cli/master/scripts/install/install.sh)"`
	wingetPackageID = "Oracle.OCI-CLI"
	// wingetNoApplicableInstaller is APPINSTALLER_CLI_ERROR_NO_APPLICABLE_INSTALLER, e.g. no user-scope installer.
	wingetNoApplicableInstaller = 0x8A150010
)

type ociCLIStep struct{}

// NewOCICLIStep returns the OCI CLI install step.
func NewOCICLIStep() Step { return ociCLIStep{} }

func (ociCLIStep) ID() string     { return "oci-cli" }
func (ociCLIStep) NeedsTTY() bool { return true }

// Done mirrors ocicli.New: a set OCI_ASSUME_OCI_BIN must exist and never falls back to PATH.
func (ociCLIStep) Done(e *Env) (bool, error) {
	if p := e.Getenv("OCI_ASSUME_OCI_BIN"); p != "" {
		_, err := os.Stat(p) // #nosec G703 -- user-set oci binary path
		return err == nil, nil
	}
	_, err := e.LookPath("oci")
	return err == nil, nil
}

// installer picks the package manager: winget on Windows, else Homebrew; "" when neither exists.
func installer(e *Env) string {
	tool := "brew"
	if e.GOOS == "windows" {
		tool = "winget"
	}
	if _, err := e.LookPath(tool); err != nil {
		return ""
	}
	return tool
}

func wingetArgs(scoped bool) []string {
	a := []string{"install", "--id", wingetPackageID, "-e"}
	if scoped {
		a = append(a, "--scope", "user")
	}
	return append(a, "--accept-source-agreements", "--accept-package-agreements")
}

// manualHint is what to do by hand when no supported installer exists.
func manualHint(goos string) string {
	switch goos {
	case "windows":
		return "winget not found; install the MSI from " + ociMSIURL
	case "linux":
		return "Homebrew not found; install with: " + ociScriptCmd
	}
	return "Homebrew not found; see " + ociDocsURL
}

func (ociCLIStep) Describe(e *Env) string {
	switch installer(e) {
	case "brew":
		return "OCI CLI not found → install with `brew install oci-cli`?"
	case "winget":
		return "OCI CLI not found → install with `winget " + strings.Join(wingetArgs(true), " ") +
			"`? (if winget has no per-user installer, it retries without --scope user)"
	}
	return "OCI CLI not found → " + manualHint(e.GOOS)
}

// SkipReason skips without prompting when there is no installer to run.
func (ociCLIStep) SkipReason(e *Env) string {
	if installer(e) == "" {
		return manualHint(e.GOOS)
	}
	return ""
}

// noApplicableInstaller matches winget's exit code, read as either signed or unsigned 32-bit.
func noApplicableInstaller(err error) bool {
	var ec interface{ ExitCode() int }
	if !errors.As(err, &ec) {
		return false
	}
	c := ec.ExitCode()
	return uint32(c) == wingetNoApplicableInstaller // #nosec G115 -- exit codes are 32-bit
}

func (ociCLIStep) Apply(e *Env, r *Receipt) error {
	ctx, cancel := context.WithTimeout(e.Context(), ociInstallTimeout)
	defer cancel()
	tool := installer(e)
	switch tool {
	case "brew":
		if err := e.ExecTTY(ctx, "brew", "install", "oci-cli"); err != nil {
			return fmt.Errorf("brew install oci-cli: %w", err)
		}
	case "winget":
		err := e.ExecTTY(ctx, "winget", wingetArgs(true)...)
		if err != nil && ctx.Err() == nil && noApplicableInstaller(err) {
			err = e.ExecTTY(ctx, "winget", wingetArgs(false)...)
		}
		if err != nil {
			return fmt.Errorf("winget install %s: %w", wingetPackageID, err)
		}
	default:
		return fmt.Errorf("%w: %s", ErrSkipped, manualHint(e.GOOS))
	}
	r.OCICLI = tool
	return nil
}

// Undo never uninstalls the OCI CLI; it only prints the reverse command.
func (ociCLIStep) Undo(e *Env, r *Receipt) error {
	cmd := ""
	switch r.OCICLI {
	case "brew":
		cmd = "brew uninstall oci-cli"
	case "winget":
		cmd = "winget uninstall --id " + wingetPackageID + " -e"
	}
	if cmd != "" {
		fmt.Fprintf(e.Out, "oci-cli was installed by setup and is kept; to remove it run: %s\n", cmd)
	}
	r.OCICLI = ""
	return nil
}
