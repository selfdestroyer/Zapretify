//go:build !windows

package platform

import (
	"os/exec"
)

// HideWindow is a no-op on non-Windows platforms.
func HideWindow(cmd *exec.Cmd) {}

// RunHidden runs a command directly on non-Windows platforms.
func RunHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}
