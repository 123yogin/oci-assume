package setup

import (
	"errors"
	"fmt"

	"github.com/123yogin/oci-assume/internal/refresher"
)

// backgroundStep installs the OS scheduler entry that runs `refresh --all --background`.
type backgroundStep struct {
	backend func(goos string) (refresher.Backend, error)
	bin     func() (string, error)
}

// NewBackgroundStep returns the background refresh step.
func NewBackgroundStep() Step {
	return &backgroundStep{backend: refresher.ForOS, bin: refresher.BinPath}
}

func (*backgroundStep) ID() string     { return "background" }
func (*backgroundStep) NeedsTTY() bool { return false }

// BackendName is the receipt's name for the scheduler on goos.
func BackendName(goos string) string {
	switch goos {
	case "darwin":
		return "launchd"
	case "linux":
		return "systemd"
	case "windows":
		return "schtasks"
	}
	return goos
}

func (s *backgroundStep) Describe(e *Env) string {
	bin, err := s.bin()
	if err != nil {
		bin = "oci-assume"
	}
	var where string
	switch e.GOOS {
	case "darwin":
		where = "launchd agent ~/Library/LaunchAgents/" + refresher.Label + ".plist (runs at login)"
	case "linux":
		where = "systemd user timer ~/.config/systemd/user/oci-assume.{service,timer} (a crontab line if systemd --user is unavailable)"
	case "windows":
		where = `Task Scheduler task \` + refresher.TaskName + " (every 30 min and at logon, via " + refresher.HelperName + " when present)"
	default:
		where = "scheduler entry"
	}
	d := fmt.Sprintf("Background refresh: install the %s\n  runs every 30 min: %s refresh --all --background", where, bin)
	if old, ok := s.installed(e); ok {
		d += "\n  replaces the existing agent"
		if old != "" {
			d += " (runs " + old + ")"
		}
	}
	return d
}

// installed reports an existing scheduler entry and, when readable, the binary it runs.
func (s *backgroundStep) installed(e *Env) (bin string, ok bool) {
	b, err := s.backend(e.GOOS)
	if err != nil {
		return "", false
	}
	st, err := b.Status()
	if (err != nil && !errors.Is(err, refresher.ErrLastRun)) || !st.Installed {
		return "", false
	}
	if br, isBR := b.(refresher.BinReporter); isBR {
		bin, _ = br.InstalledBin()
	}
	return bin, true
}

// Done means the entry is installed and the receipt records consent.
func (s *backgroundStep) Done(e *Env) (bool, error) {
	b, err := s.backend(e.GOOS)
	if errors.Is(err, refresher.ErrUnsupported) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	r, err := LoadReceipt(e.StateDir)
	if err != nil {
		return false, err
	}
	if r.Background == nil || !r.Background.Consented {
		return false, nil
	}
	st, err := b.Status()
	if err != nil && !errors.Is(err, refresher.ErrLastRun) {
		return false, nil
	}
	return st.Installed, nil
}

func (s *backgroundStep) Apply(e *Env, r *Receipt) error {
	b, err := s.backend(e.GOOS)
	if err != nil {
		return err
	}
	bin, err := s.bin()
	if err != nil {
		return err
	}
	if err := b.Install(bin); err != nil {
		return err
	}
	r.Background = &BackgroundEntry{Backend: BackendName(e.GOOS), Consented: true}
	return nil
}

// Undo removes the entry even without a receipt; removing a missing entry is a no-op.
func (s *backgroundStep) Undo(e *Env, r *Receipt) error {
	b, err := s.backend(e.GOOS)
	if errors.Is(err, refresher.ErrUnsupported) {
		r.Background = nil
		return nil
	}
	if err != nil {
		return err
	}
	_, was := s.installed(e)
	if err := b.Uninstall(); err != nil {
		return err
	}
	if was {
		fmt.Fprintf(e.Out, "Removed the background refresh job (%s)\n", BackendName(e.GOOS))
	}
	r.Background = nil
	return nil
}
