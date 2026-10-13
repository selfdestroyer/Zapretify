//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

type InstalledAppInfo struct {
	IsInstalled      bool   `json:"is_installed"`
	InstalledVersion string `json:"installed_version"`
	InstalledPath    string `json:"installed_path"`
	HasAutostart     bool   `json:"has_autostart"`
	HasService       bool   `json:"has_service"`
}

func detectExistingInstallation() InstalledAppInfo {
	info := InstalledAppInfo{}

	// 1. Check Registry Uninstall keys (HKLM and HKCU)
	roots := []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER}
	for _, root := range roots {
		k, err := registry.OpenKey(root, `Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`, registry.QUERY_VALUE)
		if err == nil {
			loc, _, _ := k.GetStringValue("InstallLocation")
			ver, _, _ := k.GetStringValue("DisplayVersion")
			_ = k.Close()

			if loc != "" {
				info.InstalledPath = loc
				info.InstalledVersion = ver
				info.IsInstalled = true
				break
			}
		}
	}

	// 2. Fallback check: standard ProgramFiles or AppData paths
	if !info.IsInstalled {
		defaultPaths := []string{
			`C:\Program Files\Zapretify`,
			filepath.Join(os.Getenv("ProgramFiles"), "Zapretify"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Zapretify"),
		}
		for _, p := range defaultPaths {
			if p == "" {
				continue
			}
			exe := filepath.Join(p, "Zapretify.exe")
			if fi, err := os.Stat(exe); err == nil && !fi.IsDir() {
				info.IsInstalled = true
				info.InstalledPath = p
				if info.InstalledVersion == "" {
					info.InstalledVersion = "1.2.1"
				}
				break
			}
		}
	}

	if info.IsInstalled {
		// 3. Detect if Autostart was configured in HKCU Run
		if kRun, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE); err == nil {
			val, _, _ := kRun.GetStringValue("Zapretify")
			_ = kRun.Close()
			if val != "" {
				info.HasAutostart = true
			}
		}

		// 4. Detect if Windows Service zapret is installed
		if kSvc, err := registry.OpenKey(registry.LOCAL_MACHINE, `System\CurrentControlSet\Services\zapret`, registry.QUERY_VALUE); err == nil {
			_ = kSvc.Close()
			info.HasService = true
		}
	}

	return info
}
