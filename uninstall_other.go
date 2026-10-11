//go:build !windows

package main

import (
	"fmt"
	"os"
)

func handleUninstall(appDir string, quiet bool) {
	fmt.Println("Удаление поддерживается только на Windows")
	os.Exit(0)
}
