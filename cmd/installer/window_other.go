//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

func runInstallerWindow(url string) {
	fmt.Printf("[Installer] Opening URL in browser: %s\n", url)
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	}
	// Keep alive
	select {}
}
