package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"zapretify/core/config"
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

// serviceState describes the root service for the UI: the app cannot bypass anything without it.
type serviceState struct {
	Installed bool   `json:"installed"`
	OK        bool   `json:"ok"`       // answers on the socket
	Outdated  bool   `json:"outdated"` // built from another app version: reinstall from this app
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

const daemonPlist = "/Library/LaunchDaemons/" + macdaemon.Label + ".plist"

var svcCache struct {
	sync.Mutex
	at    time.Time
	state *serviceState
}

// macServiceState is polled with /api/status, so the answer is cached for two seconds.
func macServiceState() any {
	svcCache.Lock()
	defer svcCache.Unlock()
	if svcCache.state != nil && time.Since(svcCache.at) < 2*time.Second {
		return svcCache.state
	}
	st := &serviceState{}
	_, err := os.Stat(daemonPlist)
	st.Installed = err == nil
	if resp, err := macdaemon.Call(macdaemon.Request{Cmd: macdaemon.CmdStatus}, 2*time.Second); err == nil {
		st.Installed, st.OK, st.Version = true, true, resp.Version
		st.Outdated = resp.Version != config.Version
	} else {
		st.Error = err.Error()
	}
	svcCache.state, svcCache.at = st, time.Now()
	return st
}

// daemonResources is Contents/Resources/daemon of this Zapretify.app and the app itself.
func daemonResources() (dir, app string, err error) {
	src := bundledBasesDir()
	if src == "" || !strings.HasSuffix(filepath.Dir(filepath.Dir(src)), ".app") {
		return "", "", fmt.Errorf("управлять службой можно только из Zapretify.app")
	}
	return filepath.Join(src, "daemon"), filepath.Dir(filepath.Dir(src)), nil
}

// runAsAdmin runs a script from the app with root rights; macOS asks for an administrator password.
func runAsAdmin(prompt, script string, args ...string) error {
	q := func(s string) string {
		return `quoted form of "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	cmd := `"/bin/bash " & ` + q(script)
	for _, a := range args {
		cmd += ` & " " & ` + q(a)
	}
	as := `do shell script ` + cmd + ` with administrator privileges with prompt "` + prompt + `"`
	out, err := exec.Command("/usr/bin/osascript", "-e", as).CombinedOutput()
	svcCache.Lock()
	svcCache.state = nil
	svcCache.Unlock()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") {
			return fmt.Errorf("установка отменена")
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// installService installs or updates the root service from Contents/Resources/daemon of this
// Zapretify.app. macOS asks for an administrator password in its own dialog.
func installService(a *App) error {
	dir, app, err := daemonResources()
	if err != nil {
		return err
	}
	for _, f := range []string{"install-daemon.sh", "zapretifyd", "utunws"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return fmt.Errorf("в Zapretify.app нет %s", f)
		}
	}
	wasRunning := a.IsZapretRunning()
	logger.Log("[Zapretify] Установка службы Zapretify...")
	if err := runAsAdmin("Zapretify устанавливает службу, которая включает обход блокировок.",
		filepath.Join(dir, "install-daemon.sh"), filepath.Join(dir, "zapretifyd"), filepath.Join(dir, "utunws"),
		strconv.Itoa(os.Getuid()), app); err != nil {
		logger.Log("[Zapretify] Служба не установлена: %v", err)
		return err
	}
	logger.Log("[Zapretify] Служба Zapretify установлена.")
	// Reinstalling restarts the service and with it the engine: bring the bypass back.
	if wasRunning {
		time.Sleep(time.Second)
		if err := a.StartZapret(""); err != nil {
			logger.Log("[Zapret] После обновления службы Zapret не запустился: %v", err)
		}
	}
	return nil
}

// uninstallService removes the root service (the bypass stops, pf rules go away) and the login item.
func uninstallService(a *App) error {
	dir, _, err := daemonResources()
	if err != nil {
		return err
	}
	logger.Log("[Zapretify] Удаление службы Zapretify...")
	if err := runAsAdmin("Zapretify удаляет службу обхода блокировок.", filepath.Join(dir, "uninstall-daemon.sh")); err != nil {
		if err.Error() == "установка отменена" {
			return fmt.Errorf("удаление отменено")
		}
		return err
	}
	_ = config.SetAppAutostart(false)
	_ = a.cfg.Update(func(c *config.Config) {
		c.App.AutostartApp = false
		c.Zapret.BootStart = false
	})
	logger.Log("[Zapretify] Служба Zapretify удалена.")
	return nil
}

// setBootStart switches «Включать обход при загрузке Mac» in the root service.
func setBootStart(a *App, enabled bool) error {
	if err := (macdaemon.DaemonEngine{}).SetBoot(enabled, a.cfg.Clone().Zapret.Strategy); err != nil {
		return err
	}
	logger.Log("[Zapret] Запуск обхода при загрузке Mac: %v", enabled)
	return nil
}
