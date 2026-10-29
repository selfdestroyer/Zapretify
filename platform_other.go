//go:build !darwin

package main

import (
	"errors"

	"zapretify/core/platform"
	"zapretify/core/zapret"
)

// defaultEngine runs winws.exe as a child process.
func defaultEngine() zapret.Engine { return zapret.GetRunner() }

// prepareAppDir: the data already sits next to the executable.
func prepareAppDir(string) {}

// addPlatformDiagnostics: all Windows checks live in platform.RunDiagnostics.
func addPlatformDiagnostics(*platform.DiagnosticReport) {}

// macServiceState: the root service exists only on macOS.
func macServiceState() any { return nil }

func installService(*App) error {
	return errors.New("служба Zapretify нужна только на macOS")
}

func setBootStart(*App, bool) error { return errors.New("только для macOS") }

func uninstallService(*App) error {
	return errors.New("служба Zapretify нужна только на macOS")
}
