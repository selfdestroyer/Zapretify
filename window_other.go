//go:build !windows

package main

var globalApp *App

func runGUIWindow(url string, title string, width int, height int, startMinimized bool, app *App) {
	globalApp = app
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

