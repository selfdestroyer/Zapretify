//go:build windows

package main

import (
	"net/url"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32DLL        = windows.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32DLL.NewProc("ShellExecuteW")
)

func openBrowser(rawURL string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "tg" {
		return
	}

	cleanURL := parsed.String()
	urlPtr, err := windows.UTF16PtrFromString(cleanURL)
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
