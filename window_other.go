//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
)

var globalApp *App

// runGUIWindow on macOS/Linux. Started by the macOS shell (Zapretify.app, ZAPRETIFY_SHELL=1) it
// hands the UI address to the shell over stdout and lives as long as the shell keeps stdin open;
// otherwise (development) the UI opens in the default browser.
func runGUIWindow(url string, title string, width int, height int, startMinimized bool, app *App) {
	globalApp = app
	if os.Getenv("ZAPRETIFY_SHELL") == "1" {
		fmt.Printf("ZAPRETIFY_URL %s\n", url)
		if startMinimized {
			fmt.Println("ZAPRETIFY_MINIMIZED")
		}
		// The shell closes our stdin when it quits (or crashes): stop the services with it.
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if !startMinimized {
		openBrowser(url)
	}
	// The UI lives in the browser, so there is no window to wait for: keep serving until
	// a signal or «Закрыть всё» ends the process (both call os.Exit).
	select {}
}

func ExitGUIWindow() {
	if globalApp != nil {
		globalApp.Shutdown()
	}
}

func TriggerTrayUpdate() {}
