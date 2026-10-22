// Package cli wires the oci-assume commands together.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/123yogin/oci-assume/internal/lock"
	"github.com/123yogin/oci-assume/internal/notify"
	"github.com/123yogin/oci-assume/internal/ocicli"
	"github.com/123yogin/oci-assume/internal/ociconfig"
	"github.com/123yogin/oci-assume/internal/picker"
	"github.com/123yogin/oci-assume/internal/refresher"
	onboard "github.com/123yogin/oci-assume/internal/setup"
	"github.com/123yogin/oci-assume/internal/shell"
	"github.com/123yogin/oci-assume/internal/state"
	"github.com/123yogin/oci-assume/internal/token"
)

// Build metadata, set via -ldflags "-X".
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

const installURL = "https://docs.oracle.com/iaas/Content/API/SDKDocs/cliinstall.htm"

// notifier delivers desktop notifications.
type notifier interface {
	Notify(title, body string) error
}

// deps holds everything a command touches outside the process, so tests can inject.
type deps struct {
	newRunner func() (ocicli.Runner, error)
	now       func() time.Time
	stdin     *os.File
	stdout    io.Writer
	stderr    io.Writer
	isTTY     func() bool
	pick      func([]picker.Item, *os.File) (string, error)
	notifier  notifier
	goos      string
	// scheduler, binPath and ociVersion back the daemon and doctor commands.
	scheduler  func(goos string) (refresher.Backend, error)
	binPath    func() (string, error)
	ociVersion func(ctx context.Context) (string, error)
	// setupSteps backs the setup and uninstall commands; execPolicy backs doctor's Windows check.
	setupSteps func(e *onboard.Env) []onboard.Step
	execPolicy func(e *onboard.Env) (ok bool, why string, err error)
	// spawn starts this binary detached (prompt --tick); logPath is the background log.
	spawn   func(args ...string) error
	logPath func() (string, error)
}

func defaultDeps() *deps {
	return &deps{
		newRunner:  ocicli.New,
		now:        time.Now,
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		isTTY:      func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		pick:       picker.Pick,
		notifier:   notify.New(),
		goos:       runtime.GOOS,
		scheduler:  refresher.ForOS,
		binPath:    refresher.BinPath,
		ociVersion: ociVersion,
		setupSteps: onboard.DefaultSteps,
		execPolicy: onboard.ExecPolicyStatus,
		spawn:      spawnDetached(defaultLogPath),
		logPath:    defaultLogPath,
	}
}

// errConfig marks configuration and usage problems (exit 4).
var errConfig = errors.New("config error")

type configErr struct{ msg string }

func (e configErr) Error() string        { return e.msg }
func (e configErr) Is(target error) bool { return target == errConfig }

func cfgErrorf(format string, a ...any) error { return configErr{fmt.Sprintf(format, a...)} }

// asConfig keeps err's chain but also marks it as a config error.
func asConfig(err error) error { return fmt.Errorf("%w%w", err, configErr{}) }

// reported errors were already printed per item; only their exit code matters.
type reported struct{ err error }

func (r reported) Error() string { return r.err.Error() }
func (r reported) Unwrap() error { return r.err }

// Execute runs the CLI with os.Args and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, defaultDeps(), os.Args[1:])
}

func run(ctx context.Context, d *deps, args []string) int {
	root := newRoot(d)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if ctx.Err() != nil {
		fmt.Fprintln(d.stderr, "oci-assume: cancelled")
		return 1
	}
	var rep reported
	if !errors.As(err, &rep) {
		printErr(d.stderr, err)
	}
	return exitCode(err)
}

func printErr(w io.Writer, err error) {
	fmt.Fprintf(w, "oci-assume: %v\n", err)
	if errors.Is(err, ocicli.ErrNotInstalled) && !errors.Is(err, ocicli.ErrNeedsLogin) {
		fmt.Fprintf(w, "install the OCI CLI: %s (or set OCI_ASSUME_OCI_BIN)\n", installURL)
	}
}

// exitCode maps errors in priority order 2, 3, 5, 4, then 1.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ocicli.ErrNeedsLogin), errors.Is(err, onboard.ErrNeedsYes):
		return 2
	case errors.Is(err, ocicli.ErrNotInstalled):
		return 3
	case errors.Is(err, lock.ErrTimeout):
		return 5
	case errors.Is(err, errConfig), errors.Is(err, ociconfig.ErrNotFound),
		errors.Is(err, token.ErrInsecurePerms), errors.Is(err, state.ErrNoStateDir):
		return 4
	}
	return 1
}

type flags struct {
	region, threshold, loginTimeout, shell string
	unset                                  bool
}

func newRoot(d *deps) *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:           "oci-assume [profile]",
		Short:         "Switch OCI CLI profiles and keep session tokens fresh",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       versionString(),
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			selfHeal(d, cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.unset {
				return runUnset(d, f, args)
			}
			return runAssume(cmd.Context(), d, f, args)
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	// R11: under the eval wrapper only export/unset lines may reach stdout.
	out := d.stdout
	if os.Getenv("OCI_ASSUME_EVAL") == "1" {
		out = d.stderr
	}
	root.SetOut(out)
	root.SetErr(d.stderr)
	root.SetIn(d.stdin)

	pf := root.PersistentFlags()
	pf.StringVarP(&f.region, "region", "r", "", "override the profile's region")
	pf.StringVar(&f.threshold, "threshold", "", "refresh when less than this remains (default 10m, env OCI_ASSUME_THRESHOLD)")
	pf.StringVar(&f.loginTimeout, "login-timeout", "5m", "how long to wait for browser login")
	root.Flags().BoolVar(&f.unset, "unset", false, "emit lines that clear the OCI variables")
	root.Flags().StringVar(&f.shell, "shell", "", `print exports for zsh|bash|fish|pwsh without the hook, e.g. eval "$(oci-assume --shell bash dev)"`)

	root.AddCommand(newStatus(d), newRefresh(d, f), newLogin(d, f), newPrompt(d), newInit(), newVersion(),
		newDaemon(d), newDoctor(d), newSetup(d), newUninstall(d))
	return root
}

// durations resolves --threshold > $OCI_ASSUME_THRESHOLD > 10m, and --login-timeout.
func (f *flags) durations() (threshold, login time.Duration, err error) {
	threshold, err = resolveThreshold(f.threshold)
	if err != nil {
		return 0, 0, err
	}
	login, err = time.ParseDuration(f.loginTimeout)
	if err != nil || login <= 0 {
		return 0, 0, cfgErrorf("invalid --login-timeout %q", f.loginTimeout)
	}
	return threshold, login, nil
}

func resolveThreshold(flag string) (time.Duration, error) {
	src, v := "--threshold", flag
	if v == "" {
		src, v = "OCI_ASSUME_THRESHOLD", os.Getenv("OCI_ASSUME_THRESHOLD")
	}
	if v == "" {
		return 10 * time.Minute, nil
	}
	t, err := time.ParseDuration(v)
	if err != nil || t <= 0 {
		return 0, cfgErrorf("invalid %s %q (want a duration like 10m)", src, v)
	}
	return t, nil
}

// hookShell picks the export dialect: --shell, else the hook's shell under OCI_ASSUME_EVAL=1 (R19).
func hookShell(d *deps, f *flags) (shell.Shell, error) {
	if f.shell != "" {
		sh, err := shell.Parse(f.shell)
		if err != nil {
			return "", cfgErrorf("invalid --shell %q (want zsh, bash, fish or pwsh)", f.shell)
		}
		return sh, nil
	}
	if os.Getenv("OCI_ASSUME_EVAL") == "1" {
		if sh, err := shell.Parse(os.Getenv("OCI_ASSUME_SHELL")); err == nil {
			return sh, nil
		}
	}
	script := `eval "$(oci-assume --shell bash <profile>)"`
	if d.goos == "windows" {
		script = "oci-assume --shell pwsh <profile> | Out-String | Invoke-Expression"
	}
	fmt.Fprintf(d.stderr, "shell hook not loaded — add: %s\nin a script, name the shell: %s\n", hookHint(d.goos), script)
	return "", reported{errors.New("shell hook not loaded")}
}

// hookHint is the rc-file line for the user's shell ($SHELL), defaulting to zsh; pwsh on Windows.
func hookHint(goos string) string {
	if goos == "windows" {
		return "oci-assume init pwsh | Out-String | Invoke-Expression"
	}
	switch filepath.Base(os.Getenv("SHELL")) {
	case "bash":
		return `eval "$(oci-assume init bash)"`
	case "fish":
		return "oci-assume init fish | source"
	case "pwsh":
		return "oci-assume init pwsh | Out-String | Invoke-Expression"
	}
	return `eval "$(oci-assume init zsh)"`
}

func loadProfiles() ([]ociconfig.Profile, error) {
	ps, err := ociconfig.Load(ociconfig.DefaultPath())
	if err != nil {
		return nil, asConfig(err)
	}
	return ps, nil
}

func findProfile(ps []ociconfig.Profile, name string) (ociconfig.Profile, error) {
	p, ok := ociconfig.Find(ps, name)
	if !ok {
		return p, cfgErrorf("unknown profile %q; list profiles with: oci-assume status", name)
	}
	return p, nil
}

// stateLock is the lock name for state RMW; no shell-safe profile name can equal it.
const stateLock = "\x00state"

// updateState runs a state read-modify-write under the state lock (R8).
func updateState(ctx context.Context, fn func(*state.State)) error {
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	release, err := lock.Acquire(lctx, stateLock)
	if err != nil {
		return err
	}
	defer release()
	s, err := state.Load()
	if err != nil {
		return err
	}
	if s.LastAssumed == nil {
		s.LastAssumed = map[string]time.Time{}
	}
	if s.LastResult == nil {
		s.LastResult = map[string]state.Result{}
	}
	fn(&s)
	return state.Save(s)
}

// lazyRunner builds the real runner on first use and remembers a build failure.
type lazyRunner struct {
	newRunner func() (ocicli.Runner, error)
	r         ocicli.Runner
	err       error
}

func (l *lazyRunner) get() (ocicli.Runner, error) {
	if l.r == nil && l.err == nil {
		l.r, l.err = l.newRunner()
	}
	return l.r, l.err
}

// surface reports a missing oci CLI as such, not as the needs-login it caused.
func (l *lazyRunner) surface(err error) error {
	if err != nil && l.err != nil && errors.Is(err, ocicli.ErrNotInstalled) {
		return l.err
	}
	return err
}

func (l *lazyRunner) Validate(ctx context.Context, p string) error {
	r, err := l.get()
	if err != nil {
		return err
	}
	return r.Validate(ctx, p)
}

func (l *lazyRunner) Refresh(ctx context.Context, p string) error {
	r, err := l.get()
	if err != nil {
		return err
	}
	return r.Refresh(ctx, p)
}

func (l *lazyRunner) Authenticate(ctx context.Context, p, region string) error {
	r, err := l.get()
	if err != nil {
		return err
	}
	return r.Authenticate(ctx, p, region)
}

// healExempt commands never self-heal: prompt paths, setup/daemon (they manage the job), doctor (it reports), trivial output.
var healExempt = map[string]bool{"prompt": true, "init": true, "__complete": true, "setup": true,
	"uninstall": true, "version": true, "help": true, "completion": true, "daemon": true, "doctor": true}

// selfHeal reinstalls a consented background job when it is missing; errors go to the log only.
func selfHeal(d *deps, cmd *cobra.Command) {
	top := cmd
	for top.HasParent() && top.Parent().HasParent() {
		top = top.Parent()
	}
	if top.HasParent() && healExempt[top.Name()] {
		return
	}
	// The scheduled job and tick children run `refresh --background`; healing there is redundant.
	if bg, err := cmd.Flags().GetBool("background"); err == nil && bg && cmd.Name() == "refresh" {
		return
	}
	dir, err := state.Dir()
	if err != nil {
		return
	}
	r, err := onboard.LoadReceipt(dir)
	if err != nil {
		logf(d, "self-heal: %v", err)
		return
	}
	if r.Background == nil || !r.Background.Consented {
		return
	}
	b, err := d.scheduler(d.goos)
	if err != nil {
		return
	}
	bin, err := d.binPath()
	if err != nil {
		logf(d, "self-heal: %v", err)
		return
	}
	// The check above is lock-free; the read-modify-write below holds the receipt lock and skips when busy.
	release, err := onboard.LockReceipt(cmd.Context(), dir)
	if err != nil {
		if !errors.Is(err, lock.ErrTimeout) {
			logf(d, "self-heal: %v", err)
		}
		return
	}
	defer release()
	if r, err = onboard.LoadReceipt(dir); err != nil {
		logf(d, "self-heal: %v", err)
		return
	}
	prevHeal, prevCheck := r.LastHeal, r.LastCheck
	_, herr := onboard.SelfHeal(&onboard.Env{GOOS: d.goos, StateDir: dir, Now: d.now}, &r, b, bin)
	logWarnings(d, b)
	if !r.LastHeal.Equal(prevHeal) || !r.LastCheck.Equal(prevCheck) {
		if err := onboard.SaveReceipt(dir, r); err != nil {
			logf(d, "self-heal: save receipt: %v", err)
		}
	}
	if herr != nil {
		logf(d, "self-heal: reinstall background refresh: %v", herr)
	}
}

// logWarnings sends a backend's install warnings to the background log.
func logWarnings(d *deps, b refresher.Backend) {
	if w, ok := b.(refresher.Warner); ok {
		for _, m := range w.Warnings() {
			logf(d, "background: %s", m)
		}
	}
}

// setConsent records the background job's consent in the receipt (daemon install/uninstall), under the receipt lock.
func setConsent(d *deps, consented bool) error {
	dir, err := state.Dir()
	if err != nil {
		return err
	}
	return onboard.UpdateReceipt(context.Background(), dir, func(r *onboard.Receipt) bool {
		if r.Background == nil {
			if !consented {
				return false
			}
			r.Background = &onboard.BackgroundEntry{}
		}
		r.Background.Backend, r.Background.Consented = onboard.BackendName(d.goos), consented
		r.Version = "1"
		return true
	})
}
