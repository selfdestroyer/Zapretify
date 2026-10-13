//go:build windows

package main

import (
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	kernel32 = windows.NewLazyDLL("kernel32.dll")
	user32   = windows.NewLazyDLL("user32.dll")
	shell32  = windows.NewLazyDLL("shell32.dll")
	dwmapi   = windows.NewLazyDLL("dwmapi.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetModuleFileNameW  = kernel32.NewProc("GetModuleFileNameW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	procSetWindowLongW      = user32.NewProc("SetWindowLongW")
	procSetClassLongPtrW    = user32.NewProc("SetClassLongPtrW")
	procSetClassLongW       = user32.NewProc("SetClassLongW")
	procCallWindowProcW     = user32.NewProc("CallWindowProcW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procShellNotifyIconW    = shell32.NewProc("Shell_NotifyIconW")
	procExtractIconExW      = shell32.NewProc("ExtractIconExW")
	procSetCurrentProcessExplicitAppUserModelID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procGetClassLongPtrW    = user32.NewProc("GetClassLongPtrW")
	procGetClassLongW       = user32.NewProc("GetClassLongW")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	GWLP_WNDPROC = ^uintptr(3) // -4
	GCLP_HICON   = ^uintptr(13) // -14

	WM_SETICON = 0x0080
	ICON_SMALL = 0
	ICON_BIG   = 1
	IMAGE_ICON = 1
	LR_SHARED  = 0x8000
	LR_LOADFROMFILE = 0x00000010
	LR_DEFAULTSIZE  = 0x00000040

	GCLP_HICONSM = ^uintptr(33) // -34

	DWMWA_USE_IMMERSIVE_DARK_MODE_BEFORE_20H1 = 19
	DWMWA_USE_IMMERSIVE_DARK_MODE             = 20
	DWMWA_CAPTION_COLOR                       = 35
	DWMWA_TEXT_COLOR                          = 36

	WM_CLOSE    = 0x0010
	WM_DESTROY  = 0x0002
	WM_COMMAND  = 0x0111
	WM_APP      = 0x8000

	WM_TRAYCALLBACK = WM_APP + 101

	WM_MOUSEMOVE     = 0x0200
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205

	SW_HIDE    = 0
	SW_RESTORE = 9
	SW_SHOW    = 5

	NIM_ADD    = 0x00000000
	NIM_MODIFY = 0x00000001
	NIM_DELETE = 0x00000002

	NIF_MESSAGE = 0x00000001
	NIF_ICON    = 0x00000002
	NIF_TIP     = 0x00000004

	MF_STRING    = 0x00000000
	MF_SEPARATOR = 0x00000800

	TPM_BOTTOMALIGN = 0x0020
	TPM_LEFTALIGN   = 0x0000

	ID_TRAY_OPEN          = 1001
	ID_TRAY_EXIT          = 1002
	ID_TRAY_TOGGLE_ZAPRET = 1003
	ID_TRAY_TOGGLE_TG     = 1004
)

type NOTIFYICONDATAW struct {
	CbSize            uint32
	HWnd              uintptr
	UID               uint32
	UFlags            uint32
	UCallbackMessage  uint32
	HIcon             uintptr
	SzTip             [128]uint16
	DwState           uint32
	DwStateMask       uint32
	SzInfo            [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle       [64]uint16
	DwInfoFlags       uint32
}

type POINT struct {
	X int32
	Y int32
}

var (
	origWndProc   uintptr
	globalWebview webview2.WebView
	trayIconData  NOTIFYICONDATAW
	isQuitting    bool
	mainHwnd      uintptr
	globalApp     *App
)

func setWindowLongPtr(hwnd uintptr, nIndex uintptr, newProc uintptr) uintptr {
	if procSetWindowLongPtrW.Find() == nil {
		r, _, _ := procSetWindowLongPtrW.Call(hwnd, nIndex, newProc)
		return r
	}
	r, _, _ := procSetWindowLongW.Call(hwnd, nIndex, newProc)
	return r
}

func getClassIcon(hwnd uintptr) uintptr {
	if procGetClassLongPtrW.Find() == nil {
		r, _, _ := procGetClassLongPtrW.Call(hwnd, GCLP_HICON)
		return r
	}
	r, _, _ := procGetClassLongW.Call(hwnd, GCLP_HICON)
	return r
}

func setClassIcon(hwnd uintptr, nIndex uintptr, hIcon uintptr) {
	if procSetClassLongPtrW.Find() == nil {
		_, _, _ = procSetClassLongPtrW.Call(hwnd, nIndex, hIcon)
		return
	}
	_, _, _ = procSetClassLongW.Call(hwnd, nIndex, hIcon)
}

func getAppIcons() (hIconBig uintptr, hIconSmall uintptr) {
	// 1. Extract from the current executable file using ExtractIconExW
	var exeBuf [windows.MAX_PATH]uint16
	n, _, _ := procGetModuleFileNameW.Call(0, uintptr(unsafe.Pointer(&exeBuf[0])), uintptr(len(exeBuf)))
	if n > 0 && procExtractIconExW.Find() == nil {
		var big, small uintptr
		ret, _, _ := procExtractIconExW.Call(
			uintptr(unsafe.Pointer(&exeBuf[0])),
			0,
			uintptr(unsafe.Pointer(&big)),
			uintptr(unsafe.Pointer(&small)),
			1,
		)
		if ret > 0 {
			if big != 0 {
				hIconBig = big
			}
			if small != 0 {
				hIconSmall = small
			}
			if hIconBig != 0 && hIconSmall != 0 {
				return hIconBig, hIconSmall
			}
		}
	}

	// 2. Load from PE resources (Group Icon ID 2, then 1, then 3)
	hMod, _, _ := procGetModuleHandleW.Call(0)
	if hMod != 0 && procLoadImageW.Find() == nil {
		for _, id := range []uintptr{2, 1, 3} {
			if hIconBig == 0 {
				ic, _, _ := procLoadImageW.Call(hMod, id, uintptr(IMAGE_ICON), 32, 32, uintptr(LR_SHARED))
				if ic != 0 {
					hIconBig = ic
				}
			}
			if hIconSmall == 0 {
				ic, _, _ := procLoadImageW.Call(hMod, id, uintptr(IMAGE_ICON), 16, 16, uintptr(LR_SHARED))
				if ic != 0 {
					hIconSmall = ic
				}
			}
		}
		if hIconBig != 0 && hIconSmall != 0 {
			return hIconBig, hIconSmall
		}
	}

	// 3. Load from disk file (assets\icon.ico or icon.ico)
	if procLoadImageW.Find() == nil {
		for _, p := range []string{"assets\\icon.ico", "icon.ico"} {
			if ptr, err := windows.UTF16PtrFromString(p); err == nil {
				if hIconBig == 0 {
					ic, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(ptr)), uintptr(IMAGE_ICON), 32, 32, uintptr(LR_LOADFROMFILE))
					if ic != 0 {
						hIconBig = ic
					}
				}
				if hIconSmall == 0 {
					ic, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(ptr)), uintptr(IMAGE_ICON), 16, 16, uintptr(LR_LOADFROMFILE))
					if ic != 0 {
						hIconSmall = ic
					}
				}
			}
		}
		if hIconBig != 0 && hIconSmall != 0 {
			return hIconBig, hIconSmall
		}
	}

	// 4. Fallback to LoadIconW
	if hMod != 0 && procLoadIconW.Find() == nil {
		for _, id := range []uintptr{2, 1, 3} {
			ic, _, _ := procLoadIconW.Call(hMod, id)
			if ic != 0 {
				if hIconBig == 0 {
					hIconBig = ic
				}
				if hIconSmall == 0 {
					hIconSmall = ic
				}
				break
			}
		}
	}

	if hIconBig != 0 && hIconSmall == 0 {
		hIconSmall = hIconBig
	}
	if hIconSmall != 0 && hIconBig == 0 {
		hIconBig = hIconSmall
	}
	return hIconBig, hIconSmall
}

func addTrayIcon(hwnd uintptr, hIcon uintptr) {
	if hIcon == 0 {
		hIcon = getClassIcon(hwnd)
	}
	trayIconData = NOTIFYICONDATAW{
		CbSize:           uint32(unsafe.Sizeof(NOTIFYICONDATAW{})),
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
		UCallbackMessage: WM_TRAYCALLBACK,
		HIcon:            hIcon,
	}
	tipStr, _ := windows.UTF16FromString("Zapretify - Защита активна")
	copy(trayIconData.SzTip[:], tipStr)
	procShellNotifyIconW.Call(uintptr(NIM_ADD), uintptr(unsafe.Pointer(&trayIconData)))
}

func removeTrayIcon() {
	if trayIconData.HWnd != 0 {
		procShellNotifyIconW.Call(uintptr(NIM_DELETE), uintptr(unsafe.Pointer(&trayIconData)))
		trayIconData.HWnd = 0
	}
}

var (
	trayTipMu     sync.Mutex
	lastTipStatus string
	lastTipCheck  time.Time
)

func TriggerTrayUpdate() {
	updateTrayTipInternal(true)
}

func updateTrayTip() {
	updateTrayTipInternal(false)
}

func updateTrayTipInternal(force bool) {
	if trayIconData.HWnd == 0 || globalApp == nil {
		return
	}
	trayTipMu.Lock()
	defer trayTipMu.Unlock()

	now := time.Now()
	if !force && now.Sub(lastTipCheck) < 400*time.Millisecond && lastTipStatus != "" {
		return
	}
	lastTipCheck = now

	var status string
	z := globalApp.IsZapretRunning()
	tg := globalApp.GetTGProxyStatus()
	if z && tg {
		status = "Zapretify - Защита активна (Zapret + TG)"
	} else if z {
		status = "Zapretify - Zapret активен"
	} else if tg {
		status = "Zapretify - Telegram прокси активен"
	} else {
		status = "Zapretify - Службы остановлены"
	}

	if status == lastTipStatus && !force {
		return
	}
	lastTipStatus = status

	tipStr, err := windows.UTF16FromString(status)
	if err != nil {
		return
	}
	var zeroTip [128]uint16
	trayIconData.SzTip = zeroTip
	copy(trayIconData.SzTip[:], tipStr)
	trayIconData.UFlags = NIF_TIP
	procShellNotifyIconW.Call(uintptr(NIM_MODIFY), uintptr(unsafe.Pointer(&trayIconData)))
}

func ExitGUIWindow() {
	isQuitting = true
	removeTrayIcon()
	if mainHwnd != 0 {
		procShowWindow.Call(mainHwnd, uintptr(SW_HIDE))
	}
	if globalApp != nil {
		globalApp.Shutdown()
	}
	if globalWebview != nil {
		globalWebview.Terminate()
	}
	if mainHwnd != 0 {
		procDestroyWindow.Call(mainHwnd)
	}
}

func trayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	switch uint32(msg) {
	case WM_CLOSE:
		if !isQuitting {
			// Minimize to tray instead of quitting
			procShowWindow.Call(hwnd, uintptr(SW_HIDE))
			return 0
		}
	case WM_TRAYCALLBACK:
		switch uint32(lParam) {
		case WM_MOUSEMOVE:
			updateTrayTip()
			return 0
		case WM_LBUTTONUP, WM_LBUTTONDBLCLK:
			procShowWindow.Call(hwnd, uintptr(SW_RESTORE))
			procSetForegroundWindow.Call(hwnd)
			return 0
		case WM_RBUTTONUP:
			updateTrayTipInternal(true)
			var pt POINT
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			hMenu, _, _ := procCreatePopupMenu.Call()
			if hMenu != 0 {
				openStr, _ := windows.UTF16PtrFromString("Открыть Zapretify")
				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_TRAY_OPEN), uintptr(unsafe.Pointer(openStr)))
				procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)

				if globalApp != nil {
					var zapretLabel string
					if globalApp.IsZapretRunning() {
						zapretLabel = "Выключить Zapret"
					} else {
						zapretLabel = "Включить Zapret"
					}
					zapretStr, _ := windows.UTF16PtrFromString(zapretLabel)
					procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_TRAY_TOGGLE_ZAPRET), uintptr(unsafe.Pointer(zapretStr)))

					var tgLabel string
					if globalApp.GetTGProxyStatus() {
						tgLabel = "Выключить Telegram прокси"
					} else {
						tgLabel = "Включить Telegram прокси"
					}
					tgStr, _ := windows.UTF16PtrFromString(tgLabel)
					procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_TRAY_TOGGLE_TG), uintptr(unsafe.Pointer(tgStr)))

					procAppendMenuW.Call(hMenu, uintptr(MF_SEPARATOR), 0, 0)
				}

				exitStr, _ := windows.UTF16PtrFromString("Выход")
				procAppendMenuW.Call(hMenu, uintptr(MF_STRING), uintptr(ID_TRAY_EXIT), uintptr(unsafe.Pointer(exitStr)))

				procSetForegroundWindow.Call(hwnd)
				procTrackPopupMenu.Call(hMenu, uintptr(TPM_BOTTOMALIGN|TPM_LEFTALIGN), uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
				procDestroyMenu.Call(hMenu)
			}
			return 0
		}
	case WM_COMMAND:
		cmdID := uint32(wParam) & 0xffff
		switch cmdID {
		case ID_TRAY_OPEN:
			procShowWindow.Call(hwnd, uintptr(SW_RESTORE))
			procSetForegroundWindow.Call(hwnd)
			return 0
		case ID_TRAY_TOGGLE_ZAPRET:
			if globalApp != nil {
				go func() {
					if globalApp.IsZapretRunning() {
						_ = globalApp.StopZapret()
					} else {
						_ = globalApp.StartZapret("")
					}
					time.Sleep(300 * time.Millisecond)
					TriggerTrayUpdate()
				}()
			}
			return 0
		case ID_TRAY_TOGGLE_TG:
			if globalApp != nil {
				go func() {
					if globalApp.GetTGProxyStatus() {
						_ = globalApp.StopTGProxy()
					} else {
						_ = globalApp.StartTGProxy()
					}
					time.Sleep(300 * time.Millisecond)
					TriggerTrayUpdate()
				}()
			}
			return 0
		case ID_TRAY_EXIT:
			isQuitting = true
			removeTrayIcon()
			if hwnd != 0 {
				procShowWindow.Call(hwnd, uintptr(SW_HIDE))
			}
			if globalApp != nil {
				globalApp.Shutdown()
			}
			if globalWebview != nil {
				globalWebview.Terminate()
			}
			procDestroyWindow.Call(hwnd)
			return 0
		}
	case 0x0016: // WM_ENDSESSION
		if wParam != 0 {
			if globalApp != nil {
				globalApp.Shutdown()
			}
		}
		return 0
	case WM_DESTROY:
		removeTrayIcon()
		if globalApp != nil {
			globalApp.Shutdown()
		}
	}

	r, _, _ := procCallWindowProcW.Call(origWndProc, hwnd, msg, wParam, lParam)
	return r
}

func enableDarkTitlebar(hwnd uintptr) {
	if procDwmSetWindowAttribute.Find() != nil {
		return
	}
	darkMode := uint32(1)
	// Try Windows 10 build 19041+ / Windows 11 (attr 20)
	ret, _, _ := procDwmSetWindowAttribute.Call(
		hwnd,
		uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE),
		uintptr(unsafe.Pointer(&darkMode)),
		uintptr(unsafe.Sizeof(darkMode)),
	)
	if ret != 0 {
		// Try Windows 10 build 1809 / 1903 (attr 19)
		_, _, _ = procDwmSetWindowAttribute.Call(
			hwnd,
			uintptr(DWMWA_USE_IMMERSIVE_DARK_MODE_BEFORE_20H1),
			uintptr(unsafe.Pointer(&darkMode)),
			uintptr(unsafe.Sizeof(darkMode)),
		)
	}

	// For Windows 11: Set title bar background color to #0b0e14 (COLORREF 0x00140e0b in 0x00BBGGRR)
	captionColor := uint32(0x00140e0b)
	_, _, _ = procDwmSetWindowAttribute.Call(
		hwnd,
		uintptr(DWMWA_CAPTION_COLOR),
		uintptr(unsafe.Pointer(&captionColor)),
		uintptr(unsafe.Sizeof(captionColor)),
	)

	// Set title bar text color to #f8fafc (COLORREF 0x00fcfaf8 in 0x00BBGGRR)
	textColor := uint32(0x00fcfaf8)
	_, _, _ = procDwmSetWindowAttribute.Call(
		hwnd,
		uintptr(DWMWA_TEXT_COLOR),
		uintptr(unsafe.Pointer(&textColor)),
		uintptr(unsafe.Sizeof(textColor)),
	)
}

func runGUIWindow(url string, title string, width int, height int, startMinimized bool, app *App) {
	globalApp = app

	// Set explicit AppUserModelID so Windows Taskbar binds icon and identity to Zapretify
	if procSetCurrentProcessExplicitAppUserModelID.Find() == nil {
		appId, _ := windows.UTF16PtrFromString("Zapretify.App.v1")
		_, _, _ = procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(appId)))
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: false,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  uint(width),
			Height: uint(height),
			IconId: 2,
		},
	})
	if w == nil {
		openBrowser(url)
		return
	}
	defer w.Destroy()

	globalWebview = w
	hwnd := uintptr(w.Window())
	mainHwnd = hwnd

	// Enable Dark Mode Titlebar
	enableDarkTitlebar(hwnd)

	// Apply App Icons (Big for Taskbar / Alt-Tab, Small for Titlebar)
	hIconBig, hIconSmall := getAppIcons()
	if hIconBig == 0 {
		hIconBig = getClassIcon(hwnd)
	}
	if hIconSmall == 0 {
		hIconSmall = hIconBig
	}
	if hIconBig != 0 {
		procSendMessageW.Call(hwnd, uintptr(WM_SETICON), uintptr(ICON_BIG), hIconBig)
		setClassIcon(hwnd, GCLP_HICON, hIconBig)
	}
	if hIconSmall != 0 {
		procSendMessageW.Call(hwnd, uintptr(WM_SETICON), uintptr(ICON_SMALL), hIconSmall)
		setClassIcon(hwnd, GCLP_HICONSM, hIconSmall)
	}

	// Add Tray Icon
	addTrayIcon(hwnd, hIconSmall)
	defer removeTrayIcon()
	TriggerTrayUpdate()

	// Background ticker to keep tray tooltip continuously synchronized
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for !isQuitting {
			<-ticker.C
			updateTrayTip()
		}
	}()

	// Subclass Window Proc for close-to-tray & tray messages
	wndProcCallback := syscall.NewCallback(trayWndProc)
	origWndProc = setWindowLongPtr(hwnd, GWLP_WNDPROC, wndProcCallback)

	w.SetTitle(title)
	w.SetSize(width, height, webview2.HintFixed)
	w.Init(`
		window.open = function(url) {
			if (url) {
				fetch('/api/open-url?url=' + encodeURIComponent(url)).catch(function(){});
			}
			return null;
		};
	`)
	w.Navigate(url)

	if startMinimized {
		procShowWindow.Call(hwnd, uintptr(SW_HIDE))
	}

	w.Run()
}
