//go:build windows

package platform

import (
	"os/exec"
	"syscall"
)

// HideWindow configures the command to run without popping up a console window on Windows.
func HideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

// RunHidden runs a command hidden on Windows without flashing a terminal window.
func RunHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	HideWindow(cmd)
	return cmd.Run()
}
