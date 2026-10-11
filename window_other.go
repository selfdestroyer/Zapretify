//go:build !windows

package main

func runGUIWindow(url string, title string, width int, height int, startMinimized bool, app *App) {
	if !startMinimized {
		openBrowser(url)
	}
}

func ExitGUIWindow() {}
