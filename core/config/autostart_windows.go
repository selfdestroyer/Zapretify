//go:build windows

package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyName = `Software\Microsoft\Windows\CurrentVersion\Run`
	appName    = "Zapretify"
	taskName   = "Zapretify"
)

func SetAppAutostart(enabled bool) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	cleanExe := filepath.Clean(exePath)
	cmdTarget := fmt.Sprintf(`"%s" --minimized`, cleanExe)

	if enabled {
		// 1. Register Task Scheduler job with highest privileges (runs elevated on logon without UAC prompts)
		cmd := exec.Command("schtasks", "/create", "/tn", taskName, "/tr", cmdTarget, "/sc", "onlogon", "/rl", "highest", "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			_ = removeRegistryAutostart()
			return nil
		}

		// 2. Fallback to registry if schtasks failed
		return setRegistryAutostart(cmdTarget)
	} else {
		// Disable in both Task Scheduler and Registry
		cmd := exec.Command("schtasks", "/delete", "/tn", taskName, "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Run()

		_ = removeRegistryAutostart()
		return nil
	}
}

func IsAppAutostart() bool {
	// 1. Check Task Scheduler
	cmd := exec.Command("schtasks", "/query", "/tn", taskName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err == nil {
		return true
	}

	// 2. Check Registry
	return isRegistryAutostart()
}

func setRegistryAutostart(cmd string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyName, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open Run key: %w", err)
	}
	defer k.Close()
	return k.SetStringValue(appName, cmd)
}

func removeRegistryAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyName, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	err = k.DeleteValue(appName)
	if err == registry.ErrNotExist {
		return nil
	}
	return err
}

func isRegistryAutostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyName, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(appName)
	return err == nil
}
