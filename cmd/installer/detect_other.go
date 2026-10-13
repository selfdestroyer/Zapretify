//go:build !windows

package main

type InstalledAppInfo struct {
	IsInstalled      bool   `json:"is_installed"`
	InstalledVersion string `json:"installed_version"`
	InstalledPath    string `json:"installed_path"`
	HasAutostart     bool   `json:"has_autostart"`
	HasService       bool   `json:"has_service"`
}

func detectExistingInstallation() InstalledAppInfo {
	return InstalledAppInfo{}
}
