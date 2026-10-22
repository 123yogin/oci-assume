// Package notify shows desktop notifications, silently doing nothing when no tool exists.
package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notifier delivers a desktop notification.
type Notifier interface {
	Notify(title, body string) error
}

const timeout = 5 * time.Second

// toastScript reads its text from env vars so nothing is interpolated into the script.
const toastScript = `$ErrorActionPreference='Stop';` +
	`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null;` +
	`$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02);` +
	`$t = $x.GetElementsByTagName('text');` +
	`[void]$t.Item(0).AppendChild($x.CreateTextNode($env:OCI_ASSUME_NOTIFY_TITLE));` +
	`[void]$t.Item(1).AppendChild($x.CreateTextNode($env:OCI_ASSUME_NOTIFY_BODY));` +
	`$id = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe';` +
	`[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show([Windows.UI.Notifications.ToastNotification]::new($x))`

type notifier struct {
	goos string
	look func(string) (string, error)
	run  func(ctx context.Context, name string, args, env []string) error
}

// New returns the notifier for this OS: osascript, notify-send or a PowerShell toast.
func New() Notifier {
	return &notifier{goos: runtime.GOOS, look: exec.LookPath, run: run}
}

func (n *notifier) Notify(title, body string) error {
	var name string
	var args, env []string
	switch n.goos {
	case "darwin":
		name = "osascript"
		args = []string{"-e", "display notification " + appleString(body) + " with title " + appleString(title)}
	case "windows":
		name = "powershell"
		args = []string{"-NoProfile", "-NonInteractive", "-Command", toastScript}
		env = []string{"OCI_ASSUME_NOTIFY_TITLE=" + title, "OCI_ASSUME_NOTIFY_BODY=" + body}
	case "linux", "freebsd", "openbsd", "netbsd":
		name = "notify-send"
		args = []string{"--", title, body}
	default:
		return nil
	}
	path, err := n.look(name)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return n.run(ctx, path, args, env)
}

// appleString renders s as an AppleScript string literal.
func appleString(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", " ", "\n", " ").Replace(s)
	return `"` + s + `"`
}

func run(ctx context.Context, name string, args, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: runs the platform notifier by design
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
