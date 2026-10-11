//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyName = `Software\Microsoft\Windows\CurrentVersion\Run`
	appName    = "Zapretify"
)

func SetAppAutostart(enabled bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyName, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open Run key: %w", err)
	}
	defer k.Close()

	if enabled {
		exePath, err := os.Executable()
		if err != nil {
			return err
		}
		cmd := fmt.Sprintf(`"%s" --minimized`, filepath.Clean(exePath))
		return k.SetStringValue(appName, cmd)
	} else {
		err := k.DeleteValue(appName)
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
}

func IsAppAutostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyName, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	_, _, err = k.GetStringValue(appName)
	return err == nil
}
