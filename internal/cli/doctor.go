package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/refresher"
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
		Short: "Check the oci CLI, config, permissions, shell hook and daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := &checker{w: cmd.OutOrStdout()}
			checkOCI(cmd.Context(), d, c)
			checkConfig(d, c)
			checkHook(d, c)
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
		c.ok("daemon installed (%s)", st.Path)
	case err != nil && !errors.Is(err, refresher.ErrLastRun):
		c.fail(fmt.Sprintf("daemon not installed (%v)", err), "oci-assume daemon install")
	default:
		c.fail("daemon not installed", "oci-assume daemon install")
	}
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
		c.warn("no last refresh recorded yet", "oci-assume daemon install")
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
