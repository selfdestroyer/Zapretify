//go:build !windows && !darwin

package platform

import "errors"

// ActiveVPNAdapters is only implemented on Windows and macOS.
func ActiveVPNAdapters() []string { return nil }

// OpenNetworkSettings is only implemented on Windows and macOS.
func OpenNetworkSettings() error {
	return errors.New("not supported on this platform")
}
