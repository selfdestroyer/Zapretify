//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessImagePath returns the full path of the executable image of the given process.
func ProcessImagePath(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

// pidsByImageName lists the PIDs of all running processes with the given executable name.
func pidsByImageName(name string) ([]uint32, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return nil, err
	}

	var pids []uint32
	for {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), name) {
			pids = append(pids, pe.ProcessID)
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return pids, nil
}

// TerminateProcess forcefully terminates a process by PID and waits (up to 3 s) for it to exit.
func TerminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}
	_, _ = windows.WaitForSingleObject(h, 3000)
	return nil
}

// KillImageInDir terminates every running process named exeName whose executable lives exactly at
// dir\exeName (the calling process itself is always skipped). Processes with the same name that were started from other folders (other tools,
// other copies of the bypass software) are left untouched. It returns the number of terminated processes.
func KillImageInDir(exeName, dir string) int {
	want := filepath.Join(dir, exeName)
	pids, err := pidsByImageName(exeName)
	if err != nil {
		return 0
	}
	self := uint32(os.Getpid())
	killed := 0
	for _, pid := range pids {
		if pid == self {
			continue // a process never terminates itself through this helper
		}
		img, err := ProcessImagePath(int(pid))
		if err != nil || !SamePath(img, want) {
			continue
		}
		if TerminateProcess(int(pid)) == nil {
			killed++
		}
	}
	return killed
}

// ForeignImages lists running processes named exeName that were not started from dir\exeName:
// copies of the bypass software from other folders that grab the same traffic.
// A process whose path cannot be read is reported only when running as administrator,
// because without admin rights that is also true of our own elevated winws.exe.
func ForeignImages(exeName, dir string) []ForeignProcess {
	want := filepath.Join(dir, exeName)
	pids, err := pidsByImageName(exeName)
	if err != nil {
		return nil
	}
	admin := IsAdmin()
	var found []ForeignProcess
	for _, pid := range pids {
		img, err := ProcessImagePath(int(pid))
		if err != nil {
			if admin {
				found = append(found, ForeignProcess{PID: int(pid)})
			}
			continue
		}
		if !SamePath(img, want) {
			found = append(found, ForeignProcess{PID: int(pid), Path: img})
		}
	}
	return found
}

// KillForeignImages terminates the processes ForeignImages reports and returns how many exited.
func KillForeignImages(exeName, dir string) int {
	killed := 0
	for _, p := range ForeignImages(exeName, dir) {
		if TerminateProcess(p.PID) == nil {
			killed++
		}
	}
	return killed
}
