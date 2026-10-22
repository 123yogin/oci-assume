package refresher

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// systemd uses a user timer, or a crontab line when `systemctl --user` is unavailable.
type systemd struct{ s *sys }

// probe reports whether systemctl --user works and the unit dir the manager searches.
// It follows the manager's XDG_CONFIG_HOME, not the shell's; without systemctl it uses the shell's.
func (d *systemd) probe() (dir string, ok bool) {
	out, err := d.s.exec("", "systemctl", "--user", "show-environment")
	if err != nil {
		return filepath.Join(d.s.configDir, "systemd", "user"), false
	}
	cfg := filepath.Join(d.s.home, ".config")
	for _, l := range strings.Split(out, "\n") {
		if v, found := strings.CutPrefix(strings.TrimSpace(l), "XDG_CONFIG_HOME="); found && v != "" {
			cfg = v
		}
	}
	return filepath.Join(cfg, "systemd", "user"), true
}

func (d *systemd) ctl(args ...string) error {
	_, err := d.s.exec("", "systemctl", append([]string{"--user"}, args...)...)
	return err
}

// Install uses the timer when systemd is available (dropping any stale cron line), else cron.
func (d *systemd) Install(bin string) error {
	path, env := d.s.getenv("PATH"), captureEnv(d.s.getenv)
	if err := validate("linux", bin, path, env); err != nil {
		return err
	}
	dir, ok := d.probe()
	if !ok {
		return d.cronInstall(cronLine(bin, path, env))
	}
	files, err := render("linux", bin, path, d.s.home, env)
	if err != nil {
		return err
	}
	for _, n := range []string{serviceName, timerName} {
		if err := writeFile(filepath.Join(dir, n), files[n], 0o644); err != nil {
			return err
		}
	}
	if err := d.ctl("daemon-reload"); err != nil {
		return err
	}
	if err := d.ctl("enable", "--now", timerName); err != nil {
		return err
	}
	return d.cronUninstall()
}

// Uninstall removes both the units (when present) and any cron marker line.
func (d *systemd) Uninstall() error {
	dir, avail := d.probe()
	timer := filepath.Join(dir, timerName)
	if exists(timer) || exists(filepath.Join(dir, serviceName)) {
		if avail && exists(timer) {
			if err := d.ctl("disable", "--now", timerName); err != nil {
				return err
			}
		}
		for _, n := range []string{timerName, serviceName} {
			if err := removeIfExists(filepath.Join(dir, n)); err != nil {
				return err
			}
		}
		if avail {
			if err := d.ctl("daemon-reload"); err != nil {
				return err
			}
		}
	}
	return d.cronUninstall()
}

func (d *systemd) Status() (Status, error) {
	var st Status
	if dir, ok := d.probe(); ok {
		st.Path = filepath.Join(dir, timerName)
		st.Installed = exists(st.Path) && d.ctl("is-enabled", timerName) == nil
	} else {
		st.Path = "crontab"
		lines, err := d.cronRead()
		if err != nil && !errors.Is(err, errNoCrontab) {
			return st, err
		}
		_, st.Installed = withoutMarker(lines)
	}
	return st, d.s.lastRun(&st)
}

// cronLine is the fallback entry; values are shell-quoted when needed and % escaped for cron.
// Output is discarded (cron would mail it); results are recorded in the state file.
func cronLine(bin, path string, env []envVar) string {
	q := func(s string) string { return strings.ReplaceAll(shQuote(s), "%", `\%`) }
	vars := "PATH=" + q(path)
	for _, e := range env {
		vars += " " + e.Key + "=" + q(e.Val)
	}
	return "*/30 * * * * " + vars + " " + q(bin) + " refresh --all --background >/dev/null 2>&1 " + cronMarker
}

func shQuote(s string) string {
	safe := s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-:+@,=") == ""
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// errNoCrontab means there is no crontab binary or no table yet; both read as empty.
var errNoCrontab = errors.New("no crontab")

// cronRead returns the crontab lines from stdout; other read failures are errors.
func (d *systemd) cronRead() ([]string, error) {
	out, err := d.s.exec("", "crontab", "-l")
	if err != nil {
		low := strings.ToLower(err.Error())
		if errors.Is(err, exec.ErrNotFound) || strings.Contains(low, "no crontab") || (strings.Contains(low, "can't open") && strings.Contains(low, "no such file")) {
			return nil, errNoCrontab
		}
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n"), nil
}

func withoutMarker(lines []string) (kept []string, found bool) {
	for _, l := range lines {
		if strings.HasSuffix(strings.TrimSpace(l), cronMarker) {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	return kept, found
}

func (d *systemd) cronWrite(lines []string) error {
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	_, err := d.s.exec(body, "crontab", "-")
	return err
}

func (d *systemd) cronInstall(line string) error {
	lines, err := d.cronRead()
	if err != nil && !errors.Is(err, errNoCrontab) {
		return err
	}
	kept, _ := withoutMarker(lines)
	return d.cronWrite(append(kept, line))
}

// cronUninstall strips marker lines; a missing crontab or no marker writes nothing.
func (d *systemd) cronUninstall() error {
	lines, err := d.cronRead()
	if errors.Is(err, errNoCrontab) {
		return nil
	}
	if err != nil {
		return err
	}
	kept, found := withoutMarker(lines)
	if !found {
		return nil
	}
	return d.cronWrite(kept)
}
