//go:build windows

package platform

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

func IsAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	token := windows.Token(0)
	member, err := token.IsMember(sid)
	if err != nil {
		return false
	}
	return member
}

func RunAsAdmin(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	verbPtr, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	argsPtr, _ := windows.UTF16PtrFromString(strings.Join(args, " "))

	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		0,
		windows.SW_NORMAL,
	)

	if ret <= 32 {
		return fmt.Errorf("ShellExecute failed with code: %d", ret)
	}
	return nil
}

// OpenFile opens a local file using the system default handler via ShellExecuteW
func OpenFile(path string) error {
	verbPtr, _ := windows.UTF16PtrFromString("open")
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		0,
		windows.SW_NORMAL,
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW failed with code %d", ret)
	}
	return nil
}
