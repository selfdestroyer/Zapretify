//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	MB_OK           = 0x00000000
	MB_OKCANCEL     = 0x00000001
	MB_YESNO        = 0x00000004
	MB_ICONINFO     = 0x00000040
	MB_ICONWARN     = 0x00000030
	MB_ICONQUESTION = 0x00000020
	MB_ICONERROR    = 0x00000010

	IDOK     = 1
	IDCANCEL = 2
	IDYES    = 6
	IDNO     = 7
)

var (
	user32DLL       = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32DLL.NewProc("MessageBoxW")
)

// ShowMessageBox displays a native Windows message box.
func ShowMessageBox(title, text string, style uint32) int {
	tPtr, _ := windows.UTF16PtrFromString(title)
	mPtr, _ := windows.UTF16PtrFromString(text)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), uintptr(style))
	return int(ret)
}
