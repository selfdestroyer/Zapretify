//go:build !windows && !darwin

package config

func SetAppAutostart(enabled bool) error {
	return nil
}

func IsAppAutostart() bool {
	return false
}
