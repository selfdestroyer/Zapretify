//go:build !windows

package platform

import "errors"

// ActiveVPNAdapters is only implemented on Windows.
func ActiveVPNAdapters() []string { return nil }

// OpenNetworkSettings is only implemented on Windows.
func OpenNetworkSettings() error {
	return errors.New("not supported on this platform")
}
