//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32DLL        = windows.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32DLL.NewProc("ShellExecuteW")
)

func openBrowser(url string) {
	urlPtr, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return
	}
	openPtr, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return
	}
	// SW_SHOWNORMAL = 1
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(openPtr)), uintptr(unsafe.Pointer(urlPtr)), 0, 0, 1)
}
