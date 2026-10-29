package config

import (
	"os"
	"path/filepath"
)

// AppDir is where Zapretify keeps config.json, the log, strategies.json, lists/ and bin/.
// On macOS the .app bundle is read-only, so data lives in ~/Library/Application Support/Zapretify;
// the root daemon imports the bases from the same place (macdaemon.UserDataSubdir).
func AppDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, "Library", "Application Support", "Zapretify")
}
