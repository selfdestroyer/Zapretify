//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"zapretify/core/platform"
	"zapretify/core/zapret"
)

func handleUninstall(appDir string, quiet bool) {
	if !platform.IsAdmin() {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	if !quiet {
		res := platform.ShowMessageBox("Удаление Zapretify", "Вы действительно хотите удалить Zapretify и все его компоненты с этого компьютера?", platform.MB_YESNO|platform.MB_ICONQUESTION)
		if res != platform.IDYES {
			os.Exit(0)
		}
	}

	myPID := os.Getpid()

	// 1. Terminate running zapret processes and other Zapretify instances
	psKill := fmt.Sprintf(`Get-Process -Name "Zapretify" -ErrorAction SilentlyContinue | Where-Object { $_.Id -ne %d } | Stop-Process -Force`, myPID)
	_ = platform.RunHidden("powershell", "-NoProfile", "-NonInteractive", "-Command", psKill)
	_ = platform.RunHidden("taskkill", "/F", "/IM", "winws.exe", "/T")
	_ = platform.RunHidden("taskkill", "/F", "/IM", "tg-ws-proxy.exe", "/T")

	// 2. Stop and remove zapret system service
	_ = zapret.RemoveService()
	_ = platform.RunHidden("sc", "stop", "zapret")
	_ = platform.RunHidden("sc", "delete", "zapret")

	// 3. Stop and delete WinDivert driver
	_ = platform.RunHidden("net", "stop", "WinDivert")
	_ = platform.RunHidden("net", "stop", "WinDivert14")
	_ = platform.RunHidden("sc", "delete", "WinDivert")
	_ = platform.RunHidden("sc", "delete", "WinDivert14")

	// 4. Remove Windows Scheduled Task
	_ = platform.RunHidden("schtasks", "/delete", "/tn", "Zapretify", "/f")

	// 5. Remove Run autorun registry keys
	_ = platform.RunHidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Zapretify", "/f")
	_ = platform.RunHidden("reg", "delete", `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Zapretify", "/f")

	// 6. Remove Uninstall registry keys
	_ = platform.RunHidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`, "/f")
	_ = platform.RunHidden("reg", "delete", `HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`, "/f")

	// 7. Remove shortcuts
	userProfile := os.Getenv("USERPROFILE")
	appData := os.Getenv("APPDATA")
	publicProfile := os.Getenv("PUBLIC")
	allUsersProfile := os.Getenv("ALLUSERSPROFILE")
	programData := os.Getenv("PROGRAMDATA")

	shortcuts := []string{
		filepath.Join(userProfile, "Desktop", "Zapretify.lnk"),
		filepath.Join(publicProfile, "Desktop", "Zapretify.lnk"),
		filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
		filepath.Join(allUsersProfile, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
		filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
	}
	for _, sc := range shortcuts {
		_ = os.Remove(sc)
	}

	// 8. Self-delete application directory via detached script in Temp
	cleanAppDir := filepath.Clean(appDir)
	vol := filepath.VolumeName(cleanAppDir)
	if cleanAppDir != "" && cleanAppDir != "/" && cleanAppDir != vol && cleanAppDir != vol+"\\" && cleanAppDir != vol+"/" {
		cleanupScript := `@echo off
chcp 65001 > nul
set /a attempts=0
:retry
set /a attempts+=1
timeout /t 1 /nobreak > nul
taskkill /F /PID %d >nul 2>&1
rd /s /q "%s" >nul 2>&1
if exist "%s" (
    if %%attempts%% lss 10 goto retry
)
del /f /q "%%~f0" >nul 2>&1
`
		cleanupBat := filepath.Join(os.TempDir(), fmt.Sprintf("zapretify_uninst_%d.bat", time.Now().UnixNano()))
		_ = os.WriteFile(cleanupBat, []byte(fmt.Sprintf(cleanupScript, myPID, cleanAppDir, cleanAppDir)), 0755)
		_ = platform.RunHidden("cmd.exe", "/c", "start", "/b", "", cleanupBat)
	}

	// 9. Show completion message if interactive
	if !quiet {
		platform.ShowMessageBox("Удаление Zapretify", "Zapretify успешно удален с вашего компьютера.", platform.MB_OK|platform.MB_ICONINFO)
	}

	os.Exit(0)
}
