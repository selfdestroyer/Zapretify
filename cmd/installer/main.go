package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
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

	"zapretify/core/config"
	"zapretify/core/platform"
)

//go:embed frontend/*
var embeddedFrontend embed.FS

//go:embed payload.zip
var payloadZip []byte

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

const browseScript = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$f = New-Object System.Windows.Forms.FolderBrowserDialog
$f.SelectedPath = $env:ZAPRETIFY_INIT_PATH
$f.Description = 'Выберите папку для установки Zapretify'
if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $f.SelectedPath }
`

var browseMu sync.Mutex

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	return json.NewDecoder(r.Body).Decode(v)
}

func newInstallerHandler() http.Handler {
	mux := http.NewServeMux()

	if distFS, err := fs.Sub(embeddedFrontend, "frontend"); err == nil {
		mux.Handle("/", http.FileServer(http.FS(distFS)))
	}

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		existing := detectExistingInstallation()
		defaultPath := getDefaultInstallPath()
		if existing.IsInstalled && existing.InstalledPath != "" {
			defaultPath = existing.InstalledPath
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"default_path":      defaultPath,
			"version":           config.Version,
			"is_installed":      existing.IsInstalled,
			"installed_version": existing.InstalledVersion,
			"installed_path":    existing.InstalledPath,
			"has_autostart":     existing.HasAutostart,
			"has_service":       existing.HasService,
		})
	})

	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var req struct {
			CurrentPath string `json:"current_path"`
		}
		_ = decodeBody(w, r, &req)

		initial := getDefaultInstallPath()
		if clean, err := validateInstallPath(req.CurrentPath); err == nil {
			initial = clean
		}

		if !browseMu.TryLock() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "диалог выбора папки уже открыт"})
			return
		}
		defer browseMu.Unlock()

		selected := initial
		if runtime.GOOS == "windows" {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
			defer cancel()
			out, err := powershellEncoded(ctx, browseScript, map[string]string{"ZAPRETIFY_INIT_PATH": initial})
			if picked := strings.TrimSpace(string(out)); err == nil && picked != "" {
				selected = picked
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"selected_path": selected})
	})

	mux.HandleFunc("/api/progress", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, snapshotProgress())
	})

	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var req InstallRequest
		if err := decodeBody(w, r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "некорректный запрос"})
			return
		}
		if err := beginInstall(req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
	})

	mux.HandleFunc("/api/finish", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var req struct {
			Launch bool `json:"launch"`
		}
		_ = decodeBody(w, r, &req)

		if dir := installedTarget(); req.Launch && dir != "" {
			cmd := exec.Command(filepath.Join(dir, "Zapretify.exe"))
			cmd.Dir = dir
			_ = cmd.Start()
		}

		go func() {
			time.Sleep(300 * time.Millisecond)
			os.Exit(0)
		}()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/exit", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if isInstalling() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "идёт установка"})
			return
		}
		go func() {
			time.Sleep(200 * time.Millisecond)
			os.Exit(0)
		}()
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

func main() {
	if runtime.GOOS == "windows" && !platform.IsAdmin() {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	token, err := newSessionToken()
	if err != nil {
		fmt.Printf("Failed to create session token: %v\n", err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Printf("Failed to bind local listener: %v\n", err)
		os.Exit(1)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	server := &http.Server{
		Handler:           guard(token, port, newInstallerHandler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()

	guiURL := fmt.Sprintf("http://127.0.0.1:%d/index.html", port)
	runInstallerWindow(guiURL)
}
