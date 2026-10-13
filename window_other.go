//go:build !windows

package main

var globalApp *App

func runGUIWindow(url string, title string, width int, height int, startMinimized bool, app *App) {
	globalApp = app
	if !startMinimized {
		openBrowser(url)
	}
}

func ExitGUIWindow() {
	if globalApp != nil {
		globalApp.Shutdown()
	}
}

func TriggerTrayUpdate() {}

