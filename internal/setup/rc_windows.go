//go:build windows

package setup

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
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
