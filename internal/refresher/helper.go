package refresher

import (
	"os"
	"path/filepath"
)

// HelperName is the windowless (-H=windowsgui) build the Windows task runs, so no console flashes.
const HelperName = "oci-assume-bg.exe"

// HelperPath is BinPath, swapped for the helper when it sits next to the main binary.
func HelperPath() (string, error) {
	bin, err := BinPath()
	if err != nil {
		return "", err
	}
	return helperFor(bin), nil
}

func helperFor(bin string) string {
	h := filepath.Join(filepath.Dir(bin), HelperName)
	if fi, err := os.Stat(h); err == nil && fi.Mode().IsRegular() {
		return h
	}
	return bin
}

// windowsUser is DOMAIN\user for the logon trigger; without one, registering it would need admin.
func windowsUser(getenv func(string) string) string {
	u, d := getenv("USERNAME"), getenv("USERDOMAIN")
	switch {
	case u == "":
		return ""
	case d == "":
		return u
	}
	return d + `\` + u
}
