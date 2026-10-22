//go:build windows

package setup

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// parentProcessName reads the parent's and grandparent's exe names from a process snapshot.
func parentProcessName() (parent, grandparent string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", ""
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	names, parents := map[uint32]string{}, map[uint32]uint32{}
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		names[pe.ProcessID] = windows.UTF16ToString(pe.ExeFile[:])
		parents[pe.ProcessID] = pe.ParentProcessID
	}
	pp := uint32(os.Getppid()) // #nosec G115 -- pids fit in uint32 on Windows
	return names[pp], names[parents[pp]]
}

// NewShellPath is the PATH Explorer gives a new shell: the machine PATH then the user PATH, expanded.
func NewShellPath() (string, error) {
	var parts []string
	for _, k := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	} {
		key, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := key.GetStringValue("Path")
		_ = key.Close()
		if err != nil {
			continue
		}
		if x, err := registry.ExpandString(v); err == nil {
			v = x
		}
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		return "", errors.New("no PATH in the registry")
	}
	return strings.Join(parts, ";"), nil
}
