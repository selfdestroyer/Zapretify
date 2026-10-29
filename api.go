package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"zapretify/core/config"
	"zapretify/core/logger"
	"zapretify/core/zapret"
)

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONStatus(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSONStatus(w, http.StatusBadRequest, map[string]string{
		"status": "error",
		"error":  msg,
	})
}

func serverError(w http.ResponseWriter, msg string) {
	writeJSONStatus(w, http.StatusInternalServerError, map[string]string{
		"status": "error",
		"error":  msg,
	})
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{
			"status": "error",
			"error":  "Method Not Allowed: POST is required",
		})
		return false
	}
	return true
}

func requireGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{
			"status": "error",
			"error":  "Method Not Allowed: GET is required",
		})
		return false
	}
	return true
}

func validStrategy(id string) bool {
	if id == "" {
		return true
	}
	_, ok := zapret.GetRegistry().Find(id)
	return ok
}

func validBenchmarkMethod(method string) bool {
	if method == "" {
		return true
	}
	switch strings.ToLower(method) {
	case "all", "dpi", "https":
		return true
	default:
		return false
	}
}

func validBenchmarkTargetGroup(group string) bool {
	if group == "" {
		return true
	}
	switch strings.ToLower(group) {
	case "all", "yt_discord", "blockcheck":
		return true
	default:
		return false
	}
}

func validIPSetMode(mode string) bool {
	switch strings.ToLower(mode) {
	case "none", "loaded", "any":
		return true
	default:
		return false
	}
}

func isAllowedURL(u *url.URL) bool {
	scheme := strings.ToLower(u.Scheme)
	if scheme == "tg" {
		host := strings.ToLower(u.Host)
		if host == "" {
			host = strings.ToLower(u.Hostname())
		}
		switch host {
		case "proxy", "resolve", "join":
			return true
		default:
			return false
		}
	}
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	allowedHosts := []string{
		"github.com",
		"api.github.com",
		"raw.githubusercontent.com",
		"t.me",
		"telegram.me",
		"telegram.org",
	}
	for _, h := range allowedHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func registerAPIRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		status := map[string]interface{}{
			"version":   config.Version,
			"zapret":    app.GetZapretStatus(),
			"tg_proxy":  app.GetTGProxyStatus(),
			"tg_link":   app.GetTGProxyLink(),
			"benchmark": app.GetBenchmarkActivity(),
			"platform":  runtime.GOOS,
			"vpn":       activeVPN(),
			"service":   macServiceState(),
		}
		writeJSON(w, status)
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
			current := app.GetConfig()
			cfgCopy := current.Clone()

			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(cfgCopy); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{
					"status": "error",
					"error":  "Invalid config JSON: " + err.Error(),
				})
				return
			}

			cfgCopy.App.GithubRepo = config.DefaultGithubRepo

			if err := cfgCopy.Validate(); err != nil {
				badRequest(w, "Config validation failed: "+err.Error())
				return
			}

			if cfgCopy.Zapret.Strategy != "" && !app.IsValidStrategy(cfgCopy.Zapret.Strategy) {
				badRequest(w, "Invalid zapret strategy ID: strategy not found in registry")
				return
			}

			if err := app.SaveConfig(cfgCopy); err != nil {
				serverError(w, err.Error())
				return
			}
		} else if r.Method != http.MethodGet {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{
				"status": "error",
				"error":  "Method Not Allowed: only GET and POST are supported",
			})
			return
		}
		writeJSON(w, app.GetConfig())
	})

	mux.HandleFunc("/api/zapret/strategies", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetStrategies())
	})

	mux.HandleFunc("/api/zapret/start", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		stratID := r.URL.Query().Get("strategy")
		if !app.IsValidStrategy(stratID) {
			badRequest(w, "Invalid strategy ID")
			return
		}
		err := app.StartZapret(stratID)
		if err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			TriggerTrayUpdate()
		}()
	})

	mux.HandleFunc("/api/zapret/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.StopZapret()
		if err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		go func() {
			time.Sleep(200 * time.Millisecond)
			TriggerTrayUpdate()
		}()
	})

	mux.HandleFunc("/api/zapret/install-service", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		stratID := r.URL.Query().Get("strategy")
		if !app.IsValidStrategy(stratID) {
			badRequest(w, "Invalid strategy ID")
			return
		}
		err := app.InstallZapretService(stratID)
		if err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/remove-service", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.RemoveZapretService()
		if err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/force-kill", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := app.ForceKillZapret(); err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/app/force-exit", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "message": "Приложение и все зависимости завершаются..."})
		if err := app.ForceExitAll(); err != nil {
			logger.Log("[App] ForceExitAll error: %v", err)
		}
	})

	mux.HandleFunc("/api/zapret/reset-drivers", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.ResetDrivers()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/benchmark", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		stratID := r.URL.Query().Get("strategy")
		if !app.IsValidStrategy(stratID) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid strategy ID"})
			return
		}
		method := r.URL.Query().Get("method")
		if !validBenchmarkMethod(method) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid benchmark method. Allowed: all, dpi, https"})
			return
		}
		targets := r.URL.Query().Get("targets")
		if !validBenchmarkTargetGroup(targets) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid benchmark targets. Allowed: all, yt_discord, blockcheck"})
			return
		}
		err := app.StartSingleBenchmark(stratID, method, targets)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "started"})
	})

	mux.HandleFunc("/api/zapret/benchmark/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetSingleBenchmarkStatus())
	})

	mux.HandleFunc("/api/zapret/benchmark-all", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		method := r.URL.Query().Get("method")
		if !validBenchmarkMethod(method) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid benchmark method. Allowed: all, dpi, https"})
			return
		}
		targets := r.URL.Query().Get("targets")
		if !validBenchmarkTargetGroup(targets) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid benchmark targets. Allowed: all, yt_discord, blockcheck"})
			return
		}
		err := app.StartAutoBenchmark(method, targets)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "started"})
	})

	mux.HandleFunc("/api/zapret/benchmark-all/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetAutoBenchmarkStatus())
	})

	mux.HandleFunc("/api/zapret/benchmark-all/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		app.StopAutoBenchmark()
		writeJSON(w, map[string]string{"status": "stopping"})
	})

	mux.HandleFunc("/api/zapret/benchmark-all/last", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		last, err := app.GetLastBenchmark()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		// null, если подбор ещё не проводился
		writeJSON(w, last)
	})

	mux.HandleFunc("/api/zapret/targets", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		group := r.URL.Query().Get("group")
		if !validBenchmarkTargetGroup(group) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid target group. Allowed: all, yt_discord, blockcheck"})
			return
		}
		writeJSON(w, app.GetTargetsByGroup(group))
	})

	mux.HandleFunc("/api/zapret/open-test-log", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.OpenTestLog()
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/test-log", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		content, err := app.GetTestLog()
		if err != nil {
			writeJSONStatus(w, http.StatusNotFound, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"content": content})
	})

	mux.HandleFunc("/api/zapret/ipset-mode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mode := r.URL.Query().Get("mode")
			if !validIPSetMode(mode) {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "Invalid ipset mode. Allowed: none, loaded, any"})
				return
			}
			if err := app.SetIPSetMode(mode); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
				return
			}
		} else if r.Method != http.MethodGet {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"status": "error", "error": "Method Not Allowed: only GET and POST are supported"})
			return
		}
		writeJSON(w, map[string]string{"mode": app.GetIPSetMode()})
	})

	mux.HandleFunc("/api/tg/start", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.StartTGProxy()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		go func() {
			time.Sleep(300 * time.Millisecond)
			TriggerTrayUpdate()
		}()
	})

	mux.HandleFunc("/api/tg/stop", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.StopTGProxy()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		go func() {
			time.Sleep(200 * time.Millisecond)
			TriggerTrayUpdate()
		}()
	})

	mux.HandleFunc("/api/tg/ping", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.PingTelegramDCs())
	})

	mux.HandleFunc("/api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetDiagnostics())
	})

	mux.HandleFunc("/api/platform/restart-admin", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.RestartAsAdmin()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/fix-tcp", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		err := app.FixTCPTimestamps()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/kill-foreign-winws", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		writeJSON(w, map[string]int{"killed": app.KillForeignWinws()})
	})

	mux.HandleFunc("/api/zapret/boot", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := setBootStart(app, r.URL.Query().Get("enabled") == "1"); err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/install-service", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := installService(app); err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/uninstall-service", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := uninstallService(app); err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/network-settings", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := app.OpenNetworkSettings(); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/updater/check", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		res, err := app.CheckAppUpdate()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/updater/assets", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		// Synchronisation can take much longer than an HTTP request may last, so it runs in the
		// background and the UI polls /api/updater/assets/status.
		if err := app.StartAssetSync(); err != nil {
			badRequest(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "started"})
	})

	mux.HandleFunc("/api/updater/assets/status", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetAssetSyncStatus())
	})

	mux.HandleFunc("/api/updater/assets/backup", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, app.GetAssetsBackup())
	})

	mux.HandleFunc("/api/updater/assets/rollback", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		res, err := app.RestoreAssetsBackup()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/lists", func(w http.ResponseWriter, r *http.Request) {
		fn := r.URL.Query().Get("file")
		if fn == "" {
			fn = "list-general.txt"
		}
		if r.Method == http.MethodPost {
			// Limit body to 5MB to avoid denial-of-service memory exhaustion
			r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
				return
			}
			if err := app.SaveList(fn, body.Content); err != nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
				return
			}
		} else if r.Method != http.MethodGet {
			writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"status": "error", "error": "Method Not Allowed"})
			return
		}

		content, err := app.ReadList(fn)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"file": fn, "content": content})
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, map[string]interface{}{"lines": app.GetLogs()})
	})

	mux.HandleFunc("/api/logs/clear", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if err := app.ClearLogs(); err != nil {
			serverError(w, err.Error())
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/debug-report", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		writeJSON(w, map[string]string{"report": app.GenerateDebugReport()})
	})

	mux.HandleFunc("/api/open-url", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "URL parameter is required"})
			return
		}

		parsed, err := url.Parse(targetURL)
		if err != nil || !isAllowedURL(parsed) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{
				"status": "error",
				"error":  "Forbidden URL scheme or host. Only trusted https, http, and tg URLs are permitted",
			})
			return
		}

		openBrowser(parsed.String())
		writeJSON(w, map[string]string{"status": "ok"})
	})
}
