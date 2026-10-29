//go:build !darwin

package config

import (
	"os"
	"path/filepath"
)

// AppDir is where Zapretify keeps config.json, the log, strategies.json, lists/ and bin/:
// next to the executable (installer and portable layouts).
func AppDir() string {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	return filepath.Dir(exePath)
}
