//go:build windows

package main

import (
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazyDLL("user32.dll")
	dwmapi   = windows.NewLazyDLL("dwmapi.dll")

	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	DWMWA_USE_IMMERSIVE_DARK_MODE_BEFORE_20H1 = 19
	DWMWA_USE_IMMERSIVE_DARK_MODE             = 20
)

func runInstallerWindow(url string) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "Установка Zapretify",
			Width:  660,
			Height: 520,
			IconId: 1,
			Center: true,
		},
	})
	if w == nil {
		return
	}
	defer w.Destroy()

	hwnd := uintptr(w.Window())
	if hwnd != 0 {
		// Apply Immersive Dark Mode for title bar
		darkMode := int32(1)
		_, _, _ = procDwmSetWindowAttribute.Call(
			hwnd,
			uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE),
			uintptr(unsafe.Pointer(&darkMode)),
			unsafe.Sizeof(darkMode),
		)
		_, _, _ = procDwmSetWindowAttribute.Call(
			hwnd,
			uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE_BEFORE_20H1),
			uintptr(unsafe.Pointer(&darkMode)),
			unsafe.Sizeof(darkMode),
		)

		w.SetSize(660, 520, webview2.HintFixed)
		procShowWindow.Call(hwnd, 1)
		procSetForegroundWindow.Call(hwnd)
	}

	w.Navigate(url)
	w.Run()
}
