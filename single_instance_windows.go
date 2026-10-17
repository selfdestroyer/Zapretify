//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL      = windows.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32DLL.NewProc("CreateMutexW")
	procCloseHandle  = kernel32DLL.NewProc("CloseHandle")

	user32DLL               = windows.NewLazyDLL("user32.dll")
	procFindWindowW         = user32DLL.NewProc("FindWindowW")
	procShowWindowSI        = user32DLL.NewProc("ShowWindow")
	procSetForegroundWindowSI = user32DLL.NewProc("SetForegroundWindow")
)

const (
	SW_SHOW_CMD    = 5
	SW_RESTORE_CMD = 9
)

func restoreExistingWindow() {
	windowTitle, _ := windows.UTF16PtrFromString("Zapretify")
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(windowTitle)))
	if hwnd != 0 {
		procShowWindowSI.Call(hwnd, uintptr(SW_SHOW_CMD))
		procShowWindowSI.Call(hwnd, uintptr(SW_RESTORE_CMD))
		procSetForegroundWindowSI.Call(hwnd)
	}
}

// ensureSingleInstance ensures only one instance of Zapretify runs at a time.
// If an instance is already running, it brings the existing window to the foreground and returns false.
func ensureSingleInstance() (func(), bool) {
	mutexName, _ := windows.UTF16PtrFromString("Local\\Zapretify_SingleInstance_Mutex")
	handle, _, err := procCreateMutexW.Call(
		0,
		0,
		uintptr(unsafe.Pointer(mutexName)),
	)

	// If handle == 0: check if error is ACCESS_DENIED or ALREADY_EXISTS (e.g. non-admin opening admin mutex)
	if handle == 0 {
		if err == syscall.Errno(windows.ERROR_ALREADY_EXISTS) || err == syscall.Errno(windows.ERROR_ACCESS_DENIED) {
			restoreExistingWindow()
			return func() {}, false
		}
		return func() {}, true
	}

	if err == syscall.Errno(windows.ERROR_ALREADY_EXISTS) || err == syscall.Errno(windows.ERROR_ACCESS_DENIED) {
		restoreExistingWindow()
		procCloseHandle.Call(handle)
		return func() {}, false
	}

	release := func() {
		procCloseHandle.Call(handle)
	}
	return release, true
}
