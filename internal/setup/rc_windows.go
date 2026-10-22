//go:build windows

package setup

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// parentProcessName finds the parent's exe name from a process snapshot.
func parentProcessName() (string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	pe := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	ppid := uint32(os.Getppid()) // #nosec G115 -- pids fit in uint32 on Windows
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == ppid {
			return windows.UTF16ToString(pe.ExeFile[:]), nil
		}
	}
	return "", err
}
