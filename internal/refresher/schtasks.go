package refresher

import (
	"encoding/binary"
	"os"
	"unicode/utf16"
)

type schtasks struct{ s *sys }

// Install registers the task from a temporary UTF-16 XML file; /F makes reinstall idempotent.
func (w *schtasks) Install(bin string) error {
	files, err := render("windows", bin, w.s.getenv("PATH"), w.s.home, captureEnv(w.s.getenv))
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(w.s.tmpDir, "oci-assume-task-*.xml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.Write(utf16le(files["task.xml"]))
	if cerr := f.Close(); werr != nil || cerr != nil {
		if werr != nil {
			return werr
		}
		return cerr
	}
	_, err = w.s.exec("", "schtasks", "/Create", "/TN", TaskName, "/XML", f.Name(), "/F")
	return err
}

func (w *schtasks) installed() bool {
	_, err := w.s.exec("", "schtasks", "/Query", "/TN", TaskName)
	return err == nil
}

func (w *schtasks) Uninstall() error {
	if !w.installed() {
		return nil
	}
	_, err := w.s.exec("", "schtasks", "/Delete", "/TN", TaskName, "/F")
	return err
}

func (w *schtasks) Status() (Status, error) {
	st := Status{Path: `\` + TaskName, Installed: w.installed()}
	return st, w.s.lastRun(&st)
}

// utf16le encodes s as UTF-16LE with a BOM, the encoding schtasks expects.
func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2+2*len(u))
	b[0], b[1] = 0xFF, 0xFE
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2+2*i:], c)
	}
	return b
}
