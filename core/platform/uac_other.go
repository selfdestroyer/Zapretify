//go:build !windows

package platform

import (
	"os/exec"
	"runtime"
)

func IsAdmin() bool {
	return false
}

func RunAsAdmin(args ...string) error {
	return nil
}

func OpenFile(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
