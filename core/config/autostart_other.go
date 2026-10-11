//go:build !windows

package config

func SetAppAutostart(enabled bool) error {
	return nil
}

func IsAppAutostart() bool {
	return false
}
