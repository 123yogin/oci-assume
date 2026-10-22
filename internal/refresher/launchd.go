package refresher

import (
	"fmt"
	"os"
	"path/filepath"
)

type launchd struct{ s *sys }

func (l *launchd) plist() string {
	return filepath.Join(l.s.home, "Library", "LaunchAgents", Label+".plist")
}

func (l *launchd) domain() string  { return fmt.Sprintf("gui/%d", l.s.uid) }
func (l *launchd) service() string { return l.domain() + "/" + Label }

func (l *launchd) loaded() bool {
	_, err := l.s.exec("", "launchctl", "print", l.service())
	return err == nil
}

// Install writes the plist and bootstraps it, booting out a loaded copy first.
func (l *launchd) Install(bin string) error {
	files, err := render("darwin", bin, l.s.getenv("PATH"), l.s.home, captureEnv(l.s.getenv))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath(l.s.home)), 0o755); err != nil {
		return err
	}
	if err := writeFile(l.plist(), files[Label+".plist"], 0o644); err != nil {
		return err
	}
	if l.loaded() {
		if _, err := l.s.exec("", "launchctl", "bootout", l.service()); err != nil {
			return err
		}
	}
	_, err = l.s.exec("", "launchctl", "bootstrap", l.domain(), l.plist())
	return err
}

// Uninstall boots the agent out if loaded, then removes the plist.
func (l *launchd) Uninstall() error {
	if l.loaded() {
		if _, err := l.s.exec("", "launchctl", "bootout", l.service()); err != nil {
			return err
		}
	}
	return removeIfExists(l.plist())
}

// Status reports installed only when the plist exists and the agent is loaded.
func (l *launchd) Status() (Status, error) {
	st := Status{Path: l.plist()}
	st.Installed = exists(st.Path) && l.loaded()
	return st, l.s.lastRun(&st)
}
