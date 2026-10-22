package refresher

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func fromUTF16(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE || len(b)%2 != 0 {
		t.Fatalf("not UTF-16LE with BOM")
	}
	u := make([]uint16, 0, len(b)/2-1)
	for i := 2; i < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:]))
	}
	return string(utf16.Decode(u))
}

func TestTaskXMLHasLogonTriggerAndHelper(t *testing.T) {
	f := &fakeExec{}
	s := testSys(t, f, nil)
	s.getenv = func(k string) string {
		return map[string]string{"PATH": testPath, "USERDOMAIN": "CORP", "USERNAME": "dev"}[k]
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "oci-assume.exe")
	mkExe(t, bin)
	mkExe(t, filepath.Join(dir, HelperName))
	var xml string
	s.exec = func(stdin, name string, args ...string) (string, error) {
		if len(args) > 4 && args[0] == "/Create" {
			b, err := os.ReadFile(args[4])
			if err != nil {
				t.Fatal(err)
			}
			xml = fromUTF16(t, b)
		}
		return f.run(stdin, name, args...)
	}
	if err := (&schtasks{s}).Install(bin); err != nil {
		t.Fatal(err)
	}
	eq(t, f.calls[1:], []string{"schtasks /Run /TN " + TaskName})
	got := strings.ReplaceAll(xml, filepath.Join(dir, HelperName), `C:\bin\`+HelperName)
	golden := filepath.Join("testdata", "golden", "windows-helper", "task.xml")
	if *update {
		if err := writeFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%s: %v (run with -update)", golden, err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Fatalf("mismatch:\n--- got\n%s\n--- want\n%s", got, want)
	}
	for _, s := range []string{"<LogonTrigger>", `<UserId>CORP\dev</UserId>`, "<TimeTrigger>", HelperName + "</Command>"} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %q", s)
		}
	}
}

func TestHelperPathResolution(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "oci-assume.exe")
	mkExe(t, bin)
	if got := helperFor(bin); got != bin {
		t.Fatalf("no helper: got %s", got)
	}
	helper := filepath.Join(dir, HelperName)
	mkExe(t, helper)
	if got := helperFor(bin); got != helper {
		t.Fatalf("got %s, want %s", got, helper)
	}
	// A directory with the helper's name is not the helper.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, HelperName), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := helperFor(filepath.Join(other, "oci-assume.exe")); got != filepath.Join(other, "oci-assume.exe") {
		t.Fatalf("dir used as helper: %s", got)
	}
	if p, err := HelperPath(); err != nil || p == "" {
		t.Fatalf("HelperPath: %q %v", p, err)
	}
}

func TestWindowsUser(t *testing.T) {
	cases := []struct{ dom, user, want string }{{"CORP", "dev", `CORP\dev`}, {"", "dev", "dev"}, {"CORP", "", ""}}
	for _, c := range cases {
		get := func(k string) string { return map[string]string{"USERDOMAIN": c.dom, "USERNAME": c.user}[k] }
		if got := windowsUser(get); got != c.want {
			t.Fatalf("%+v: got %q", c, got)
		}
	}
}

func TestInstalledBin(t *testing.T) {
	bin := `/opt/my apps/o"ci%$'x/oci-assume`
	t.Run("launchd", func(t *testing.T) {
		f := &fakeExec{rules: []rule{{"launchctl print", "", errExit}}}
		l := &launchd{testSys(t, f, nil)}
		if err := l.Install(bin); err != nil {
			t.Fatal(err)
		}
		if got, err := l.InstalledBin(); err != nil || got != bin {
			t.Fatalf("got %q %v", got, err)
		}
	})
	t.Run("systemd", func(t *testing.T) {
		d := &systemd{testSys(t, &fakeExec{}, nil)}
		if err := d.Install(bin); err != nil {
			t.Fatal(err)
		}
		if got, err := d.InstalledBin(); err != nil || got != bin {
			t.Fatalf("got %q %v", got, err)
		}
	})
	t.Run("cron", func(t *testing.T) {
		line := cronLine(bin, testPath, []envVar{{"OCI_ASSUME_STATE_DIR", "/s p/a'c"}})
		f := &fakeExec{rules: []rule{{"systemctl", "", errExit}, {"crontab -l", "0 * * * * other\n" + line + "\n", nil}}}
		d := &systemd{testSys(t, f, nil)}
		if got, err := d.InstalledBin(); err != nil || got != bin {
			t.Fatalf("got %q %v", got, err)
		}
	})
	t.Run("schtasks", func(t *testing.T) {
		out := "<Task><Actions><Exec><Command>C:\\a &amp; b\\oci-assume-bg.exe</Command></Exec></Actions></Task>"
		f := &fakeExec{rules: []rule{{"schtasks /Query", out, nil}}}
		w := &schtasks{testSys(t, f, nil)}
		if got, err := w.InstalledBin(); err != nil || got != `C:\a & b\oci-assume-bg.exe` {
			t.Fatalf("got %q %v", got, err)
		}
	})
	t.Run("absent", func(t *testing.T) {
		l := &launchd{testSys(t, &fakeExec{}, nil)}
		if got, err := l.InstalledBin(); err == nil {
			t.Fatalf("got %q", got)
		}
	})
}
