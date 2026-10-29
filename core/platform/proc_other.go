//go:build !windows

package platform

import "errors"

// ProcessImagePath is only implemented on Windows.
func ProcessImagePath(pid int) (string, error) {
	return "", errors.New("process image lookup is not supported on this platform")
}

// TerminateProcess is only implemented on Windows.
func TerminateProcess(pid int) error {
	return errors.New("not supported on this platform")
}

// KillImageInDir is a no-op outside Windows.
func KillImageInDir(exeName, dir string) int { return 0 }

// ForeignImages is only implemented on Windows.
func ForeignImages(exeName, dir string) []ForeignProcess { return nil }

// KillForeignImages is a no-op outside Windows.
func KillForeignImages(exeName, dir string) int { return 0 }
