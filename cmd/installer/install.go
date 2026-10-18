package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"zapretify/core/config"
	"zapretify/core/platform"
)

type InstallRequest struct {
	Path            string `json:"path"`
	DesktopShortcut bool   `json:"desktop_shortcut"`
	StartMenu       bool   `json:"start_menu"`
	Autostart       bool   `json:"autostart"`
	InstallService  bool   `json:"install_service"`
	IsUpgrade       bool   `json:"is_upgrade"`
}

type ProgressState struct {
	Percent  int      `json:"percent"`
	Status   string   `json:"status"`
	Detail   string   `json:"detail"`
	Complete bool     `json:"complete"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

var (
	progressMu     sync.RWMutex
	progressState  = ProgressState{Status: "Ожидание запуска..."}
	installRunning bool
	installedDir   string // set only after a successful installation
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

func addWarning(msg string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressState.Warnings = append(progressState.Warnings, msg)
}

func snapshotProgress() ProgressState {
	progressMu.RLock()
	defer progressMu.RUnlock()
	st := progressState
	st.Warnings = append([]string(nil), progressState.Warnings...)
	return st
}

func isInstalling() bool {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return installRunning
}

func installedTarget() string {
	progressMu.RLock()
	defer progressMu.RUnlock()
	return installedDir
}

// beginInstall validates the request, resets the progress state and starts the installation in the
// background. Only one installation can run at a time.
func beginInstall(req InstallRequest) error {
	clean, err := validateInstallPath(req.Path)
	if err != nil {
		return err
	}
	req.Path = clean

	progressMu.Lock()
	if installRunning {
		progressMu.Unlock()
		return fmt.Errorf("установка уже выполняется")
	}
	installRunning = true
	installedDir = ""
	progressState = ProgressState{Status: "Подготовка...", Detail: clean}
	progressMu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				setError(fmt.Sprintf("Внутренняя ошибка установщика: %v", r))
			}
			progressMu.Lock()
			installRunning = false
			progressMu.Unlock()
		}()
		runInstallation(req)
	}()
	return nil
}

func finishSuccess(targetDir string, isUpgrade bool) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressState.Percent = 100
	if isUpgrade {
		progressState.Status = "Обновление успешно завершено"
	} else {
		progressState.Status = "Установка успешно завершена"
	}
	progressState.Complete = true
	installedDir = targetDir
}

// ---- payload extraction ----

// skipPayloadFile reports files that must never be copied to the installation folder:
// developer/doc files, and config.json (it is created by the application on first start with a
// fresh random Telegram secret; shipping a fixed one would make every installation share it).
func skipPayloadFile(rel string) bool {
	lower := strings.ToLower(rel)
	base := path.Base(lower)
	return lower == "config.json" ||
		strings.HasSuffix(base, ".md") ||
		base == "build_all.bat" ||
		base == "license" ||
		base == "test_results.txt" ||
		strings.HasPrefix(base, "test_") ||
		strings.HasPrefix(base, ".git")
}

// isUserOwnedFile reports files that belong to the user and must never be overwritten:
// lists/*-user.txt and custom.txt. They are only created when missing.
func isUserOwnedFile(rel string) bool {
	lower := strings.ToLower(rel)
	if path.Base(lower) == "custom.txt" {
		return true
	}
	return strings.HasPrefix(lower, "lists/") && strings.HasSuffix(lower, "-user.txt")
}

// extractPayload unpacks the zip into targetDir and returns the names of files that could not be written.
func extractPayload(zr *zip.Reader, targetDir string, progress func(pct int, name string)) []string {
	var failed []string
	total := len(zr.File)

	for i, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
			continue
		}
		rel := path.Clean(name)
		if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		if skipPayloadFile(rel) {
			continue
		}

		dest := filepath.Join(targetDir, filepath.FromSlash(rel))
		if !isWithin(targetDir, dest) {
			continue
		}

		if progress != nil {
			progress(15+int(float64(i)/float64(total)*55), rel)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0755); err != nil {
				failed = append(failed, rel)
			}
			continue
		}

		if isUserOwnedFile(rel) {
			if _, err := os.Stat(dest); err == nil {
				continue // keep the user's file
			}
		}

		if err := writeZipEntry(f, dest); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", rel, err))
		}
	}
	return failed
}

func writeZipEntry(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp := dest + ".zsetup"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---- installation ----

func stopRunningInstance(targetDir string) {
	binDir := filepath.Join(targetDir, "bin")

	// Only the processes that live inside THIS installation are terminated.
	platform.KillImageInDir("Zapretify.exe", targetDir)
	platform.KillImageInDir("winws.exe", binDir)
	platform.KillImageInDir("tg-ws-proxy.exe", targetDir)

	// The "zapret" service is stopped only if it runs binaries from this installation.
	if out, err := platform.OutputHidden("sc", "qc", "zapret"); err == nil &&
		strings.Contains(strings.ToLower(string(out)), strings.ToLower(targetDir)) {
		_ = platform.RunHidden("net", "stop", "zapret")
		platform.KillImageInDir("winws.exe", binDir)
	}

	// Release the driver files so they can be replaced (fails harmlessly if another tool uses WinDivert).
	_ = platform.RunHidden("net", "stop", "WinDivert")
	_ = platform.RunHidden("net", "stop", "WinDivert14")
	time.Sleep(300 * time.Millisecond)
}

func runInstallation(req InstallRequest) {
	targetDir := req.Path

	updateProgress(5, "Остановка активных процессов и драйверов...", "Завершаются только процессы этой установки")
	stopRunningInstance(targetDir)

	updateProgress(10, "Подготовка каталогов...", targetDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		low := strings.ToLower(err.Error())
		if strings.Contains(low, "access is denied") || strings.Contains(low, "отказано в доступе") {
			setError(fmt.Sprintf("Недостаточно прав для создания папки \"%s\". Запустите установщик от имени Администратора или выберите другую папку.", targetDir))
		} else {
			setError(fmt.Sprintf("Не удалось создать папку %s: %v", targetDir, err))
		}
		return
	}

	updateProgress(15, "Извлечение файлов дистрибутива...", "Распаковка встроенного архива")
	zr, err := zip.NewReader(bytes.NewReader(payloadZip), int64(len(payloadZip)))
	if err != nil {
		setError(fmt.Sprintf("Ошибка чтения архива дистрибутива: %v", err))
		return
	}
	if failed := extractPayload(zr, targetDir, func(pct int, name string) {
		updateProgress(pct, "Извлечение компонентов...", name)
	}); len(failed) > 0 {
		shown := failed
		if len(shown) > 5 {
			shown = shown[:5]
		}
		msg := fmt.Sprintf("Не удалось записать файлов: %d.\n%s", len(failed), strings.Join(shown, "\n"))
		if len(failed) > len(shown) {
			msg += "\n…"
		}
		msg += "\n\nЗакройте программы, использующие файлы Zapretify (в т.ч. антивирус), и повторите установку."
		setError(msg)
		return
	}

	// Remove legacy/extraneous developer files from previous installs
	for _, legacy := range []string{"ARCHITECTURE.md", "README.md", "LICENSE", "build_all.bat", "test_results.txt"} {
		_ = os.Remove(filepath.Join(targetDir, legacy))
	}

	exePath := filepath.Join(targetDir, "Zapretify.exe")

	updateProgress(75, "Создание ярлыков Windows...", "Рабочий стол и меню Пуск")
	if req.DesktopShortcut {
		if err := createShortcut(platform.DesktopDir(), exePath, targetDir); err != nil {
			addWarning(fmt.Sprintf("Ярлык на рабочем столе не создан: %v", err))
		}
	}
	if req.StartMenu {
		if err := createShortcut(platform.StartMenuProgramsDir(), exePath, targetDir); err != nil {
			addWarning(fmt.Sprintf("Ярлык в меню Пуск не создан: %v", err))
		}
	}

	if req.Autostart {
		updateProgress(85, "Настройка параметров автозапуска...", "Планировщик заданий Windows")
		if err := setupAutostart(exePath); err != nil {
			addWarning(fmt.Sprintf("Автозапуск не настроен: %v", err))
		}
	}

	updateProgress(90, "Создание деинсталлятора...", "Регистрация в Windows Settings")
	createUninstaller(targetDir)

	if req.InstallService {
		updateProgress(95, "Установка системной службы...", "zapret service")
		cmd := exec.Command(exePath, "--install-service=general")
		cmd.Dir = targetDir
		platform.HideWindow(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			addWarning(fmt.Sprintf("Служба zapret не установлена: %v %s", err, strings.TrimSpace(string(out))))
		}
	}

	time.Sleep(300 * time.Millisecond)
	finishSuccess(targetDir, req.IsUpgrade)
}

// setupAutostart registers a highest-privilege logon task. The legacy HKCU Run entry is only used
// as a fallback when the task cannot be created (and is removed otherwise, so the app never starts twice).
func setupAutostart(exePath string) error {
	cmdTarget := fmt.Sprintf(`"%s" --minimized`, exePath)
	taskErr := platform.RunHidden("schtasks", "/create", "/tn", "Zapretify", "/tr", cmdTarget, "/sc", "onlogon", "/rl", "highest", "/f")
	if taskErr == nil {
		_ = platform.RunHidden("reg", "delete", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Zapretify", "/f")
		return nil
	}
	if err := platform.RunHidden("reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Zapretify", "/t", "REG_SZ", "/d", cmdTarget, "/f"); err != nil {
		return fmt.Errorf("планировщик: %v; реестр: %v", taskErr, err)
	}
	return nil
}

// powershellEncoded runs a fixed script via -EncodedCommand; user-controlled values are passed only
// through environment variables, never interpolated into the script text.
func powershellEncoded(ctx context.Context, script string, env map[string]string) ([]byte, error) {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, 0, len(u16)*2)
	for _, c := range u16 {
		buf = binary.LittleEndian.AppendUint16(buf, c)
	}
	encoded := base64.StdEncoding.EncodeToString(buf)

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-STA", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	platform.HideWindow(cmd)
	return cmd.Output()
}

const shortcutScript = `$ErrorActionPreference = 'Stop'
$lnk = Join-Path $env:ZAPRETIFY_LNK_DIR 'Zapretify.lnk'
$ws = New-Object -ComObject WScript.Shell
$s = $ws.CreateShortcut($lnk)
$s.TargetPath = $env:ZAPRETIFY_TARGET
$s.WorkingDirectory = $env:ZAPRETIFY_WORKDIR
$s.IconLocation = $env:ZAPRETIFY_TARGET + ',0'
$s.Save()
try { $b = [System.IO.File]::ReadAllBytes($lnk); $b[0x15] = $b[0x15] -bor 0x20; [System.IO.File]::WriteAllBytes($lnk, $b) } catch {}
`

// createShortcut creates Zapretify.lnk in dir ("run as administrator" flag is set).
func createShortcut(dir, targetExe, workDir string) error {
	if dir == "" {
		return fmt.Errorf("не удалось определить папку")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := powershellEncoded(ctx, shortcutScript, map[string]string{
		"ZAPRETIFY_LNK_DIR": dir,
		"ZAPRETIFY_TARGET":  targetExe,
		"ZAPRETIFY_WORKDIR": workDir,
	})
	return err
}

func createUninstaller(targetDir string) {
	exePath := filepath.Join(targetDir, "Zapretify.exe")

	uninstallBat := "@echo off\r\nchcp 65001 > nul\r\ncd /d \"%~dp0\"\r\nstart \"\" \"Zapretify.exe\" --uninstall\r\n"
	_ = os.WriteFile(filepath.Join(targetDir, "uninstall.bat"), []byte(uninstallBat), 0755)

	// Register in Add/Remove programs (both HKCU and HKLM so Windows Settings always finds it)
	for _, key := range []string{
		`HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`,
		`HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\Zapretify`,
	} {
		add := func(name, typ, value string) {
			_ = platform.RunHidden("reg", "add", key, "/v", name, "/t", typ, "/d", value, "/f")
		}
		add("DisplayName", "REG_SZ", "Zapretify")
		add("DisplayVersion", "REG_SZ", config.Version)
		add("Publisher", "REG_SZ", "selfdestroyer")
		add("InstallLocation", "REG_SZ", targetDir)
		add("DisplayIcon", "REG_SZ", filepath.Join(targetDir, "assets", "icon.ico"))
		add("UninstallString", "REG_SZ", fmt.Sprintf("\"%s\" --uninstall", exePath))
		add("QuietUninstallString", "REG_SZ", fmt.Sprintf("\"%s\" --uninstall --quiet", exePath))
		add("URLInfoAbout", "REG_SZ", "https://github.com/selfdestroyer/Zapretify")
		add("NoModify", "REG_DWORD", "1")
		add("NoRepair", "REG_DWORD", "1")
	}
}
