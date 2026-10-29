package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zapretify/core/logger"
	"zapretify/core/macdaemon"
	"zapretify/core/platform"
	"zapretify/core/zapret"
)

// defaultEngine controls utunws through the root daemon zapretifyd.
func defaultEngine() zapret.Engine { return macdaemon.DaemonEngine{} }

// prepareAppDir creates ~/Library/Application Support/Zapretify on first launch and fills in what is
// missing there from the bases shipped with the app (Contents/Resources in the .app, the executable's
// folder in development). Existing files are never overwritten: they may be newer (updater) or edited.
func prepareAppDir(appDir string) {
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		logger.Log("[Zapretify] Не удалось создать папку данных %s: %v", appDir, err)
		return
	}
	src := bundledBasesDir()
	if src == "" {
		return
	}
	copyMissing(filepath.Join(src, "strategies.json"), filepath.Join(appDir, "strategies.json"))
	for _, sub := range []struct{ dir, ext string }{{"lists", ".txt"}, {"bin", ".bin"}} {
		entries, err := os.ReadDir(filepath.Join(src, sub.dir))
		if err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Join(appDir, sub.dir), 0o755)
		for _, e := range entries {
			if e.Type().IsRegular() && (strings.HasSuffix(e.Name(), sub.ext) || strings.HasSuffix(e.Name(), ".txt.backup")) {
				copyMissing(filepath.Join(src, sub.dir, e.Name()), filepath.Join(appDir, sub.dir, e.Name()))
			}
		}
	}
}

func bundledBasesDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, d := range []string{filepath.Join(dir, "..", "Resources"), dir} {
		if _, err := os.Stat(filepath.Join(d, "strategies.json")); err == nil {
			return filepath.Clean(d)
		}
	}
	return ""
}

func copyMissing(src, dst string) {
	if _, err := os.Lstat(dst); err == nil {
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		logger.Log("[Zapretify] Не удалось скопировать %s: %v", filepath.Base(dst), err)
	}
}

// addPlatformDiagnostics adds the macOS checks: the root service and other engines (ZapretMac).
func addPlatformDiagnostics(r *platform.DiagnosticReport) {
	state := ""
	if _, err := macdaemon.Call(macdaemon.Request{Cmd: macdaemon.CmdStatus}, 3*time.Second); err != nil {
		state = err.Error()
		r.OverallStatus = "ERROR"
	}
	r.EngineService = &state

	ours := filepath.Join(macdaemon.RootDir, "engine", "utunws")
	out, _ := exec.Command("pgrep", "-x", "utunws").Output()
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		args, _ := exec.Command("ps", "-o", "args=", "-p", f).Output()
		path := strings.TrimSpace(string(args))
		if strings.HasPrefix(path, ours+" ") {
			continue
		}
		if i := strings.Index(path, "/utunws"); i >= 0 {
			path = path[:i+len("/utunws")]
		}
		r.ForeignWinws = append(r.ForeignWinws, platform.ForeignProcess{PID: pid, Path: path})
		r.OverallStatus = "ERROR"
	}
}
