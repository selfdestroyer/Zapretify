//go:build windows

package main

import "unsafe"

// Размер окна задаётся размером клиентской области в логических пикселях (96 DPI = 100%).
// go-webview2 SetSize работает в физических пикселях и считает рамку по WS_OVERLAPPEDWINDOW,
// поэтому при масштабе 125–200% окно получалось меньше макета, а рамка неверной.
// Здесь размер пересчитывается под DPI монитора и под реальный стиль окна.

var (
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procAdjustWindowRectExForDpi = user32.NewProc("AdjustWindowRectExForDpi")
	procAdjustWindowRectEx       = user32.NewProc("AdjustWindowRectEx")
	procGetWindowLongW           = user32.NewProc("GetWindowLongW")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetClientRect            = user32.NewProc("GetClientRect")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
)

const (
	GWL_STYLE   = ^uintptr(15) // -16
	GWL_EXSTYLE = ^uintptr(19) // -20

	WM_DPICHANGED = 0x02E0

	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020

	MONITOR_DEFAULTTONEAREST = 2

	baseDPI = 96
)

type RECT struct {
	Left, Top, Right, Bottom int32
}

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

// Логический размер клиентской области, нужен для пересчёта при WM_DPICHANGED.
var clientLogicalW, clientLogicalH int

func windowDPI(hwnd uintptr) uint32 {
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
			return uint32(dpi)
		}
	}
	return baseDPI
}

func scaleForDPI(v int, dpi uint32) int32 {
	return int32((int64(v)*int64(dpi) + baseDPI/2) / baseDPI)
}

// outerSizeForClient возвращает внешний размер окна, при котором клиентская область
// равна width×height логических пикселей на мониторе с указанным DPI.
func outerSizeForClient(hwnd uintptr, width, height int, dpi uint32) (int32, int32) {
	style, _, _ := procGetWindowLongW.Call(hwnd, GWL_STYLE)
	exStyle, _, _ := procGetWindowLongW.Call(hwnd, GWL_EXSTYLE)

	r := RECT{Right: scaleForDPI(width, dpi), Bottom: scaleForDPI(height, dpi)}
	if procAdjustWindowRectExForDpi.Find() == nil {
		_, _, _ = procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&r)), style, 0, exStyle, uintptr(dpi))
	} else {
		_, _, _ = procAdjustWindowRectEx.Call(uintptr(unsafe.Pointer(&r)), style, 0, exStyle)
	}
	return r.Right - r.Left, r.Bottom - r.Top
}

// fitWindowToClient выставляет размер окна под клиентскую область width×height
// (логические пиксели) и центрирует окно в рабочей области монитора.
func fitWindowToClient(hwnd uintptr, width, height int) {
	clientLogicalW, clientLogicalH = width, height
	dpi := windowDPI(hwnd)
	ww, wh := outerSizeForClient(hwnd, width, height, dpi)

	x, y := int32(0), int32(0)
	flags := uintptr(SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED)
	mi := MONITORINFO{CbSize: uint32(unsafe.Sizeof(MONITORINFO{}))}
	monitor, _, _ := procMonitorFromWindow.Call(hwnd, MONITOR_DEFAULTTONEAREST)
	if ok, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&mi))); monitor != 0 && ok != 0 {
		work := mi.RcWork
		x = work.Left + max(0, (work.Right-work.Left-ww)/2)
		y = work.Top + max(0, (work.Bottom-work.Top-wh)/2)
	} else {
		flags |= SWP_NOMOVE
	}
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), flags)

	// Контрольный проход: у некоторых тем и сборок Windows рамка отличается от расчётной.
	// Добираем разницу, чтобы клиентская область совпала с макетом пиксель в пиксель.
	var client RECT
	if ok, _, _ := procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok != 0 {
		dx := scaleForDPI(width, dpi) - (client.Right - client.Left)
		dy := scaleForDPI(height, dpi) - (client.Bottom - client.Top)
		if dx != 0 || dy != 0 {
			_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, uintptr(ww+dx), uintptr(wh+dy),
				SWP_NOMOVE|SWP_NOZORDER|SWP_NOACTIVATE)
		}
	}
}

// handleDPIChanged пересчитывает размер окна при переносе на монитор с другим масштабом.
// Позицию берём из прямоугольника, предложенного системой, размер считаем сами.
func handleDPIChanged(hwnd, wParam, lParam uintptr) {
	if clientLogicalW == 0 || clientLogicalH == 0 {
		return
	}
	dpi := uint32(wParam & 0xffff)
	if dpi == 0 {
		dpi = windowDPI(hwnd)
	}
	ww, wh := outerSizeForClient(hwnd, clientLogicalW, clientLogicalH, dpi)
	// lParam указывает на RECT, выделенный системой на время обработки сообщения.
	suggested := (*RECT)(unsafe.Add(nil, lParam))
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(suggested.Left), uintptr(suggested.Top),
		uintptr(ww), uintptr(wh), SWP_NOZORDER|SWP_NOACTIVATE)
}
