package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"zapretify/core/platform"
)

//go:embed frontend/*
var embeddedFrontend embed.FS

//go:embed payload.zip
var payloadZip []byte

type InstallRequest struct {
	Path            string `json:"path"`
	DesktopShortcut bool   `json:"desktop_shortcut"`
	StartMenu       bool   `json:"start_menu"`
	Autostart       bool   `json:"autostart"`
	InstallService  bool   `json:"install_service"`
	IsUpgrade       bool   `json:"is_upgrade"`
}

type ProgressState struct {
	Percent  int    `json:"percent"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Complete bool   `json:"complete"`
	Error    string `json:"error,omitempty"`
}

var (
	progressMu    sync.RWMutex
	progressState = ProgressState{
		Percent: 0,
		Status:  "Ожидание запуска...",
	}
	installedTargetDir string
)

func updateProgress(percent int, status string, detail string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressState.Percent = percent
	progressState.Status = status
	progressState.Detail = detail
}

func setError(errMsg string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressState.Error = errMsg
}

func setComplete(isUpgrade bool) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressState.Percent = 100
	if isUpgrade {
		progressState.Status = "Обновление успешно завершено"
	} else {
		progressState.Status = "Установка успешно завершена"
	}
	progressState.Complete = true
}

func getDefaultInstallPath() string {
	if !platform.IsAdmin() {
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp != "" {
			return filepath.Join(localApp, "Programs", "Zapretify")
		}
	}
	pf := os.Getenv("ProgramFiles")
	if pf != "" {
		return filepath.Join(pf, "Zapretify")
	}
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp != "" {
		return filepath.Join(localApp, "Programs", "Zapretify")
	}
	return `C:\Program Files\Zapretify`
}

func main() {
	if runtime.GOOS == "windows" && !platform.IsAdmin() {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("Failed to bind local listener: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()

	// Frontend files
	distFS, err := fs.Sub(embeddedFrontend, "frontend")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(distFS)))
	}

	// API Handlers
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		existing := detectExistingInstallation()
		defaultPath := getDefaultInstallPath()
		if existing.IsInstalled && existing.InstalledPath != "" {
			defaultPath = existing.InstalledPath
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"default_path":      defaultPath,
			"version":           "1.3.0",
			"is_installed":      existing.IsInstalled,
			"installed_version": existing.InstalledVersion,
			"installed_path":    existing.InstalledPath,
			"has_autostart":     existing.HasAutostart,
			"has_service":       existing.HasService,
		})
	})

	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CurrentPath string `json:"current_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.CurrentPath == "" {
			req.CurrentPath = getDefaultInstallPath()
		}

		// Run folder browser via PowerShell
		ps := fmt.Sprintf(`[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null; $f = New-Object System.Windows.Forms.FolderBrowserDialog; $f.SelectedPath = '%s'; $f.Description = 'Выберите папку для установки Zapretify'; if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $f.SelectedPath }`, req.CurrentPath)
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Output()
		selected := strings.TrimSpace(string(out))
		if err != nil || selected == "" {
			selected = req.CurrentPath
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"selected_path": selected,
		})
	})

	mux.HandleFunc("/api/progress", func(w http.ResponseWriter, r *http.Request) {
		progressMu.RLock()
		st := progressState
		progressMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
	})

	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		var req InstallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		installedTargetDir = req.Path

		go runInstallation(req)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/finish", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Launch bool `json:"launch"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req.Launch && installedTargetDir != "" {
			exePath := filepath.Join(installedTargetDir, "Zapretify.exe")
			_ = exec.Command(exePath).Start()
		}

		go func() {
			time.Sleep(300 * time.Millisecond)
			os.Exit(0)
		}()
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/exit", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(0)
		}()
		w.WriteHeader(http.StatusOK)
	})

	server := &http.Server{Handler: mux}
	go func() {
		_ = server.Serve(listener)
	}()

	guiURL := fmt.Sprintf("http://127.0.0.1:%d/index.html", port)
	runInstallerWindow(guiURL)
}

func runInstallation(req InstallRequest) {
	targetDir := req.Path

	// 0. Cleanly stop any running instances of Zapretify or its background services
	updateProgress(5, "Остановка активных процессов...", "Завершение Zapretify и служб")
	_ = exec.Command("taskkill", "/F", "/IM", "Zapretify.exe").Run()
	_ = exec.Command("taskkill", "/F", "/IM", "winws.exe").Run()
	_ = exec.Command("taskkill", "/F", "/IM", "tg-ws-proxy.exe").Run()
	_ = exec.Command("net", "stop", "zapret").Run()
	time.Sleep(300 * time.Millisecond)

	updateProgress(10, "Подготовка каталогов...", targetDir)

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "access is denied") || strings.Contains(strings.ToLower(err.Error()), "отказано в доступе") {
			setError(fmt.Sprintf("Недостаточно прав для создания папки \"%s\". Пожалуйста, запустите установщик от имени Администратора (ПКМ -> Запуск от имени администратора) или выберите другую папку для установки.", targetDir))
		} else {
			setError(fmt.Sprintf("Не удалось создать папку %s: %v", targetDir, err))
		}
		return
	}

	// 1. Unzip payload
	updateProgress(15, "Извлечение файлов дистрибутива...", "Распаковка встроенного архива")
	zipReader, err := zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
	if err != nil {
		setError(fmt.Sprintf("Ошибка чтения архива дистрибутива: %v", err))
		return
	}

	totalFiles := len(zipReader.File)
	for i, f := range zipReader.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") {
			continue
		}

		baseName := strings.ToLower(filepath.Base(cleanName))
		// Filter out extraneous developer/doc files from installation folder
		if strings.HasSuffix(baseName, ".md") ||
			strings.EqualFold(baseName, "build_all.bat") ||
			strings.EqualFold(baseName, "license") ||
			strings.EqualFold(baseName, "test_results.txt") ||
			strings.HasPrefix(baseName, "test_") ||
			strings.HasPrefix(baseName, ".git") {
			continue
		}

		destPath := filepath.Join(targetDir, cleanName)

		// Preserve user's existing config and custom lists on upgrade
		if req.IsUpgrade {
			if _, err := os.Stat(destPath); err == nil {
				if strings.EqualFold(baseName, "config.json") || strings.EqualFold(baseName, "custom.txt") {
					continue
				}
			}
		}

		pct := 15 + int(float64(i)/float64(totalFiles)*55)
		updateProgress(pct, "Извлечение компонентов...", cleanName)

		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(destPath, 0755)
			continue
		}

		_ = os.MkdirAll(filepath.Dir(destPath), 0755)
		rc, err := f.Open()
		if err != nil {
			continue
		}

		out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err == nil {
			_, _ = io.Copy(out, rc)
			_ = out.Close()
		}
		_ = rc.Close()
	}

	// Clean up any legacy or extraneous developer files from previous installs
	for _, legacy := range []string{"ARCHITECTURE.md", "README.md", "LICENSE", "build_all.bat", "test_results.txt"} {
		_ = os.Remove(filepath.Join(targetDir, legacy))
	}

	exePath := filepath.Join(targetDir, "Zapretify.exe")

	// 2. Shortcuts
	updateProgress(75, "Создание ярлыков Windows...", "Рабочий стол и меню Пуск")
	if req.DesktopShortcut {
		desktopDir := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
		createShortcut(exePath, filepath.Join(desktopDir, "Zapretify.lnk"), targetDir)
	}
	if req.StartMenu {
		startDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs")
		_ = os.MkdirAll(startDir, 0755)
		createShortcut(exePath, filepath.Join(startDir, "Zapretify.lnk"), targetDir)
	}

	// 3. Autostart
	if req.Autostart {
		updateProgress(85, "Настройка параметров автозапуска...", "Планировщик заданий Windows")
		cmdTarget := fmt.Sprintf(`"%s" --minimized`, exePath)
		_ = exec.Command("schtasks", "/create", "/tn", "Zapretify", "/tr", cmdTarget, "/sc", "onlogon", "/rl", "highest", "/f").Run()
		_ = exec.Command("reg", "add", "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run", "/v", "Zapretify", "/t", "REG_SZ", "/d", cmdTarget, "/f").Run()
	}

	// 4. Create Uninstaller script & Add/Remove Programs entry
	updateProgress(90, "Создание деинсталлятора...", "Регистрация в Windows Settings")
	createUninstaller(targetDir)

	// 5. Service mode (optional)
	if req.InstallService {
		updateProgress(95, "Установка системной службы...", "zapret service")
		_ = exec.Command(exePath, "--install-service=general").Run()
	}

	time.Sleep(300 * time.Millisecond)
	setComplete(req.IsUpgrade)
}

func createShortcut(targetExe, lnkPath, workDir string) {
	ps := fmt.Sprintf(`$ws = New-Object -ComObject WScript.Shell; $s = $ws.CreateShortcut('%s'); $s.TargetPath = '%s'; $s.WorkingDirectory = '%s'; $s.IconLocation = '%s,0'; $s.Save(); try { $b = [System.IO.File]::ReadAllBytes('%s'); $b[0x15] = $b[0x15] -bor 0x20; [System.IO.File]::WriteAllBytes('%s', $b) } catch {}`,
		lnkPath, targetExe, workDir, targetExe, lnkPath, lnkPath)
	_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
}

func createUninstaller(targetDir string) {
	exePath := filepath.Join(targetDir, "Zapretify.exe")

	// Write helper uninstall.bat in the targetDir
	uninstallBat := `@echo off
chcp 65001 > nul
cd /d "%~dp0"
start "" "Zapretify.exe" --uninstall
`
	uninstPath := filepath.Join(targetDir, "uninstall.bat")
	_ = os.WriteFile(uninstPath, []byte(uninstallBat), 0755)

	// Register in Add/Remove programs (both HKCU and HKLM so Windows Settings always finds it)
	uninstKeys := []string{
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`,
		`HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`,
	}
	for _, uninstKey := range uninstKeys {
		_ = exec.Command("reg", "add", uninstKey, "/v", "DisplayName", "/t", "REG_SZ", "/d", "Zapretify", "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "DisplayVersion", "/t", "REG_SZ", "/d", "1.3.0", "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "Publisher", "/t", "REG_SZ", "/d", "selfdestroyer", "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "InstallLocation", "/t", "REG_SZ", "/d", targetDir, "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "DisplayIcon", "/t", "REG_SZ", "/d", filepath.Join(targetDir, "assets", "icon.ico"), "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "UninstallString", "/t", "REG_SZ", "/d", fmt.Sprintf("\"%s\" --uninstall", exePath), "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "QuietUninstallString", "/t", "REG_SZ", "/d", fmt.Sprintf("\"%s\" --uninstall --quiet", exePath), "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "URLInfoAbout", "/t", "REG_SZ", "/d", "https://github.com/selfdestroyer/Zapretify", "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "NoModify", "/t", "REG_DWORD", "/d", "1", "/f").Run()
		_ = exec.Command("reg", "add", uninstKey, "/v", "NoRepair", "/t", "REG_DWORD", "/d", "1", "/f").Run()
	}
}
