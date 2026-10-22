package notify

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
	env  []string
}

func fake(goos string, found bool) (*notifier, *[]call) {
	var calls []call
	n := &notifier{
		goos: goos,
		look: func(name string) (string, error) {
			if !found {
				return "", errors.New("not found")
			}
			return "/bin/" + name, nil
		},
		run: func(ctx context.Context, name string, args, env []string) error {
			if _, ok := ctx.Deadline(); !ok {
				return errors.New("no timeout")
			}
			calls = append(calls, call{name, args, env})
			return nil
		},
	}
	return n, &calls
}

func TestNotifyNoopWhenUnavailable(t *testing.T) {
	t.Setenv("PATH", "")
	if err := New().Notify("oci-assume", "hello"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	for _, goos := range []string{"darwin", "linux", "windows", "plan9"} {
		n, calls := fake(goos, false)
		if err := n.Notify("t", "b"); err != nil || len(*calls) != 0 {
			t.Fatalf("%s: err %v calls %v", goos, err, *calls)
		}
	}
}

func TestNotifyDarwinEscapes(t *testing.T) {
	n, calls := fake("darwin", true)
	if err := n.Notify(`ti"tle\`, "body \"x\"\nnext"); err != nil {
		t.Fatal(err)
	}
	want := call{"/bin/osascript", []string{"-e", `display notification "body \"x\" next" with title "ti\"tle\\"`}, nil}
	if !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("got %#v\nwant %#v", (*calls)[0], want)
	}
}

func TestNotifyLinux(t *testing.T) {
	n, calls := fake("linux", true)
	if err := n.Notify("-t", "$(rm -rf)"); err != nil {
		t.Fatal(err)
	}
	want := call{"/bin/notify-send", []string{"--", "-t", "$(rm -rf)"}, nil}
	if !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("got %#v", (*calls)[0])
	}
}

func TestNotifyWindowsUsesEnv(t *testing.T) {
	n, calls := fake("windows", true)
	if err := n.Notify("T'itle", "B\"ody"); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.name != "/bin/powershell" || !reflect.DeepEqual(c.args[:3], []string{"-NoProfile", "-NonInteractive", "-Command"}) {
		t.Fatalf("got %#v", c)
	}
	if strings.Contains(c.args[3], "T'itle") || strings.Contains(c.args[3], "B\"ody") {
		t.Fatalf("text interpolated into script: %s", c.args[3])
	}
	if !reflect.DeepEqual(c.env, []string{"OCI_ASSUME_NOTIFY_TITLE=T'itle", "OCI_ASSUME_NOTIFY_BODY=B\"ody"}) {
		t.Fatalf("env %v", c.env)
	}
}

func TestNotifyRunErrorReturned(t *testing.T) {
	n, _ := fake("linux", true)
	n.run = func(context.Context, string, []string, []string) error { return errors.New("boom") }
	if err := n.Notify("t", "b"); err == nil {
		t.Fatal("want error")
	}
}
