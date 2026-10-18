//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zapretify/core/platform"
	"zapretify/core/zapret"
)

// uninstallFiles and uninstallDirs are the ONLY things the uninstaller removes from the application
// folder. The folder itself is removed only if it becomes empty, so a portable copy that lives in a
// folder shared with other files (Downloads, Desktop, ...) never loses unrelated data.
var (
	uninstallFiles = []string{
		"uninstall.bat", "config.json", "config.json.tmp", "config.json.bad",
		"strategies.json", "strategies.json.tmp", "zapretify.log", "test_results.txt", "winws.pid",
		"Zapretify.manifest",
	}
	uninstallDirs = []string{"bin", "lists", "assets"}
)

func handleUninstall(appDir string, quiet bool) {
	if !platform.IsAdmin() {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	cleanAppDir := filepath.Clean(appDir)
	vol := filepath.VolumeName(cleanAppDir)
	if cleanAppDir == "" || cleanAppDir == "." || cleanAppDir == vol || cleanAppDir == vol+`\` || cleanAppDir == vol+"/" {
		if !quiet {
			platform.ShowMessageBox("Удаление Zapretify", "Не удалось определить папку программы. Удаление отменено.", platform.MB_OK|platform.MB_ICONINFO)
		}
		os.Exit(1)
	}

	if !quiet {
		msg := "Вы действительно хотите удалить Zapretify и все его компоненты с этого компьютера?\n\n" +
			"Будут удалены службы, драйвер WinDivert, автозапуск и файлы программы в папке:\n" + cleanAppDir +
			"\n\nПосторонние файлы в этой папке останутся нетронутыми."
		res := platform.ShowMessageBox("Удаление Zapretify", msg, platform.MB_YESNO|platform.MB_ICONQUESTION)
		if res != platform.IDYES {
			os.Exit(0)
		}
	}

	myPID := os.Getpid()
	exePath, err := os.Executable()
	if err != nil {
		exePath = filepath.Join(cleanAppDir, "Zapretify.exe")
	}
	exeName := filepath.Base(exePath)
	binDir := filepath.Join(cleanAppDir, "bin")

	// 1. Terminate OUR running processes only (other Zapretify copies / other tools are left alone)
	platform.KillImageInDir(exeName, cleanAppDir)
	platform.KillImageInDir("winws.exe", binDir)
	platform.KillImageInDir("tg-ws-proxy.exe", cleanAppDir)

	// 2. Stop and remove zapret system service
	_ = zapret.RemoveService(binDir)
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

	// Known-folder paths follow OneDrive redirection; the %VAR% based paths are kept as a fallback.
	shortcuts := []string{
		filepath.Join(platform.DesktopDir(), "Zapretify.lnk"),
		filepath.Join(platform.PublicDesktopDir(), "Zapretify.lnk"),
		filepath.Join(platform.StartMenuProgramsDir(), "Zapretify.lnk"),
		filepath.Join(platform.CommonStartMenuProgramsDir(), "Zapretify.lnk"),
		filepath.Join(userProfile, "Desktop", "Zapretify.lnk"),
		filepath.Join(publicProfile, "Desktop", "Zapretify.lnk"),
		filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
		filepath.Join(allUsersProfile, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
		filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs", "Zapretify.lnk"),
	}
	for _, sc := range shortcuts {
		if filepath.Base(sc) != "Zapretify.lnk" || filepath.Dir(sc) == "." {
			continue // an empty known folder collapsed the path
		}
		_ = os.Remove(sc)
	}

	// 8. Delete application files via a detached script (the running exe cannot delete itself).
	cleanupBat := filepath.Join(os.TempDir(), fmt.Sprintf("zapretify_uninst_%d.bat", time.Now().UnixNano()))
	_ = os.WriteFile(cleanupBat, []byte(buildCleanupScript(myPID, cleanAppDir, exeName)), 0755)
	_ = platform.RunHidden("cmd.exe", "/c", "start", "/b", "", cleanupBat)

	// 9. Show completion message if interactive
	if !quiet {
		platform.ShowMessageBox("Удаление Zapretify", "Zapretify успешно удален с вашего компьютера.", platform.MB_OK|platform.MB_ICONINFO)
	}

	os.Exit(0)
}

// buildCleanupScript creates a batch script that removes exactly the known application files and
// folders and finally the application folder itself if (and only if) it is empty.
func buildCleanupScript(pid int, appDir, exeName string) string {
	q := func(p string) string { return `"` + strings.ReplaceAll(p, `"`, "") + `"` }

	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("chcp 65001 > nul\r\n")
	b.WriteString("set /a attempts=0\r\n")
	b.WriteString(":retry\r\n")
	b.WriteString("set /a attempts+=1\r\n")
	b.WriteString("ping -n 2 127.0.0.1 > nul\r\n")
	b.WriteString(fmt.Sprintf("taskkill /F /PID %d >nul 2>&1\r\n", pid))
	b.WriteString(fmt.Sprintf("del /f /q %s >nul 2>&1\r\n", q(filepath.Join(appDir, exeName))))
	for _, f := range uninstallFiles {
		b.WriteString(fmt.Sprintf("del /f /q %s >nul 2>&1\r\n", q(filepath.Join(appDir, f))))
	}
	for _, d := range uninstallDirs {
		b.WriteString(fmt.Sprintf("rd /s /q %s >nul 2>&1\r\n", q(filepath.Join(appDir, d))))
	}
	// "rd" without /s removes the folder only when it is empty.
	b.WriteString(fmt.Sprintf("rd %s >nul 2>&1\r\n", q(appDir)))
	b.WriteString(fmt.Sprintf("if exist %s (\r\n", q(filepath.Join(appDir, exeName))))
	b.WriteString("    if %attempts% lss 10 goto retry\r\n")
	b.WriteString(")\r\n")
	b.WriteString("del /f /q \"%~f0\" >nul 2>&1\r\n")
	return b.String()
}
