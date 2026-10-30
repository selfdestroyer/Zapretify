package main

import "runtime"

// pfText picks the macOS wording for a message whose Windows text names Windows-only parts
// (winws.exe, the Windows service, WinDivert); other platforms keep the Windows text.
func pfText(windows, mac string) string {
	if runtime.GOOS == "darwin" {
		return mac
	}
	return windows
}
