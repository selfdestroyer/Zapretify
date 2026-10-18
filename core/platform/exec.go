package platform

import "os/exec"

// OutputHidden runs a command without flashing a console window and returns its combined output.
func OutputHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	HideWindow(cmd)
	return cmd.CombinedOutput()
}
