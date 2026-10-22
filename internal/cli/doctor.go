package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/refresher"
	onboard "github.com/123yogin/oci-assume/internal/setup"
	"github.com/123yogin/oci-assume/internal/shell"
	"github.com/123yogin/oci-assume/internal/state"
)

// checker prints one line per check and remembers whether any failed.
type checker struct {
	w      io.Writer
	failed bool
}

func (c *checker) ok(format string, a ...any) { fmt.Fprintf(c.w, "✓ "+format+"\n", a...) }

func (c *checker) fail(check, fix string) {
	c.failed = true
	fmt.Fprintf(c.w, "✗ %s — fix: %s\n", check, fix)
}

func (c *checker) warn(check, fix string) { fmt.Fprintf(c.w, "! %s — fix: %s\n", check, fix) }

func newDoctor(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the oci CLI, config, permissions, shell hook, setup receipt and daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := &checker{w: cmd.OutOrStdout()}
			e, err := setupEnv(cmd, d, "")
			if err != nil {
				return err
			}
			checkOCI(cmd.Context(), d, c)
			checkConfig(d, c)
			checkHookFile(e, c)
			checkHook(d, c)
			if d.goos == "windows" {
				checkExecPolicy(d, e, c)
			}
			checkReceipt(e, c)
			checkDaemon(d, c)
			checkLastResult(d, c)
			if c.failed {
				return reported{errors.New("doctor found problems")}
			}
			return nil
		},
	}
}

func checkOCI(ctx context.Context, d *deps, c *checker) {
	v, err := d.ociVersion(ctx)
	if err != nil {
		c.fail(fmt.Sprintf("oci CLI: %v", err), "install it from "+installURL+" or set OCI_ASSUME_OCI_BIN")
		return
	}
	c.ok("oci CLI %s", v)
}

// ociVersion resolves oci like ocicli.New ($OCI_ASSUME_OCI_BIN, then PATH) and runs --version.
func ociVersion(ctx context.Context) (string, error) {
	bin := os.Getenv("OCI_ASSUME_OCI_BIN")
	if bin != "" {
		if _, err := os.Stat(bin); err != nil { //nolint:gosec // G703: user-set oci binary path
			return "", fmt.Errorf("%w: %s", ocicli.ErrNotInstalled, bin)
		}
	} else {
		p, err := exec.LookPath("oci")
		if err != nil {
			return "", ocicli.ErrNotInstalled
		}
		bin = p
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version") //nolint:gosec // G702: runs the user-configured oci binary
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", bin, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func checkConfig(d *deps, c *checker) {
	path := ociconfig.DefaultPath()
	ps, err := ociconfig.Load(path)
	if err != nil {
		c.fail(fmt.Sprintf("config %s: %v", path, err), "oci setup config (or set OCI_CLI_CONFIG_FILE)")
		return
	}
	c.ok("config %s (%d profiles)", path, len(ps))
	for _, p := range ps {
		if !p.IsSession() {
			continue
		}
		var bad, missing []string
		for _, f := range []string{p.SecurityTokenFile, p.KeyFile} {
			fi, err := os.Stat(f)
			switch {
			case f == "":
			case err != nil:
				missing = append(missing, f)
			case d.goos != "windows" && fi.Mode().Perm()&0o077 != 0:
				bad = append(bad, f)
			}
		}
		if len(missing) > 0 {
			c.fail(fmt.Sprintf("%s: missing %s", p.Name, strings.Join(missing, ", ")), assumeFix(p.Name))
			continue
		}
		if len(bad) > 0 {
			c.fail(fmt.Sprintf("%s: readable by group/others: %s", p.Name, strings.Join(bad, ", ")), "chmod 600 "+strings.Join(bad, " "))
			continue
		}
		c.ok("%s: token and key permissions", p.Name)
	}
}

func checkHook(d *deps, c *checker) {
	if sh, err := shell.Parse(os.Getenv("OCI_ASSUME_SHELL")); err == nil {
		c.ok("shell hook loaded (%s)", sh)
		return
	}
	c.fail("shell hook not loaded", hookHint(d.goos))
}

// checkHookFile looks for the block in the active hook's shell, else the detected one.
func checkHookFile(e *onboard.Env, c *checker) {
	sh := ""
	if s, err := shell.Parse(os.Getenv("OCI_ASSUME_SHELL")); err == nil {
		sh = string(s)
	}
	sh, missing, err := onboard.HookCheck(e, sh)
	switch {
	case onboard.IsNoShell(err):
		c.warn(fmt.Sprintf("shell hook file: %v", err), "oci-assume setup --shell zsh|bash|fish|pwsh")
	case err != nil:
		c.fail(fmt.Sprintf("shell hook file: %v", err), "fix the file by hand, then oci-assume setup")
	case len(missing) > 0:
		c.fail("shell hook block missing from "+strings.Join(missing, ", "), "oci-assume setup")
	default:
		c.ok("shell hook in %s config", sh)
	}
}

// checkExecPolicy: a blocking policy stops the PowerShell profile, so the hook never loads.
func checkExecPolicy(d *deps, e *onboard.Env, c *checker) {
	ok, why, err := d.execPolicy(e)
	switch {
	case err != nil:
		c.warn(fmt.Sprintf("PowerShell execution policy unknown: %v", err), "Get-ExecutionPolicy -List")
	case why != "":
		c.warn(why, "ask your administrator")
	case !ok:
		c.fail("PowerShell execution policy blocks the profile", "Set-ExecutionPolicy RemoteSigned -Scope CurrentUser")
	default:
		c.ok("PowerShell execution policy allows the profile")
	}
}

func checkReceipt(e *onboard.Env, c *checker) {
	r, problems, err := onboard.ReceiptProblems(e)
	switch {
	case err != nil:
		c.fail(fmt.Sprintf("receipt: %v", err), "oci-assume uninstall, then oci-assume setup")
	case r.Version == "":
		c.warn("setup has not been run", "oci-assume setup")
	case len(problems) > 0:
		for _, p := range problems {
			c.fail("receipt: "+p, "oci-assume setup")
		}
	default:
		c.ok("receipt matches %d file(s)", len(r.Files))
	}
}

func checkDaemon(d *deps, c *checker) {
	b, err := d.scheduler(d.goos)
	if errors.Is(err, refresher.ErrUnsupported) {
		c.warn("daemon: not supported on "+d.goos, "run oci-assume refresh --all from your own scheduler")
		return
	}
	if err != nil {
		c.fail(fmt.Sprintf("daemon: %v", err), "oci-assume daemon install")
		return
	}
	// A state-read error belongs to the last-refresh check, so only Installed counts here.
	st, err := b.Status()
	switch {
	case st.Installed:
		if p := installedBin(b); p != "" {
			c.fail(fmt.Sprintf("daemon runs %s, which does not exist", p), "oci-assume setup")
			return
		}
		c.ok("daemon installed (%s)", st.Path)
	case err != nil && !errors.Is(err, refresher.ErrLastRun):
		c.fail(fmt.Sprintf("daemon not installed (%v)", err), "oci-assume setup")
	default:
		c.fail("daemon not installed", "oci-assume setup")
	}
}

// installedBin returns the job's binary path when it no longer exists, else "".
func installedBin(b refresher.Backend) string {
	br, ok := b.(refresher.BinReporter)
	if !ok {
		return ""
	}
	p, err := br.InstalledBin()
	if err != nil || p == "" {
		return ""
	}
	if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
		return p
	}
	return ""
}

// checkLastResult only warns: a stale or failed background run is not a broken setup.
func checkLastResult(d *deps, c *checker) {
	s, err := state.Load()
	if err != nil {
		c.warn(fmt.Sprintf("last refresh unknown: %v", err), "remove the corrupt state file; the next refresh rewrites it")
		return
	}
	var name string
	var last state.Result
	for n, r := range s.LastResult {
		// Only the scheduled job's results say whether the daemon works (M13).
		if r.Background && r.At.After(last.At) {
			name, last = n, r
		}
	}
	age := d.now().Sub(last.At)
	switch {
	case name == "":
		c.warn("no last refresh recorded yet", "oci-assume setup")
	case !last.OK:
		c.warn(fmt.Sprintf("last refresh failed for %s (%s ago): %s", name, ago(age), last.Msg), assumeFix(name))
	case age >= time.Hour:
		c.warn(fmt.Sprintf("last refresh %s ago", ago(age)), "oci-assume daemon status")
	default:
		c.ok("last refresh %s ago: ok", ago(age))
	}
}

func assumeFix(profile string) string {
	if strings.HasPrefix(profile, "-") {
		return "oci-assume login -- " + profile
	}
	return "oci-assume login " + profile
}
