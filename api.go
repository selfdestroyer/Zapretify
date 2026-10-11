package main

import (
	"encoding/json"
	"net/http"

	"zapretify/core/config"
)

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(data)
}

func registerAPIRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		status := map[string]interface{}{
			"zapret":   app.GetZapretStatus(),
			"tg_proxy": app.GetTGProxyStatus(),
			"tg_link":  app.GetTGProxyLink(),
		}
		writeJSON(w, status)
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var cfg config.Config
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = app.SaveConfig(&cfg)
		}
		writeJSON(w, app.GetConfig())
	})

	mux.HandleFunc("/api/zapret/strategies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.GetStrategies())
	})

	mux.HandleFunc("/api/zapret/start", func(w http.ResponseWriter, r *http.Request) {
		stratID := r.URL.Query().Get("strategy")
		err := app.StartZapret(stratID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/stop", func(w http.ResponseWriter, r *http.Request) {
		_ = app.StopZapret()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/install-service", func(w http.ResponseWriter, r *http.Request) {
		stratID := r.URL.Query().Get("strategy")
		err := app.InstallZapretService(stratID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/remove-service", func(w http.ResponseWriter, r *http.Request) {
		_ = app.RemoveZapretService()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/force-kill", func(w http.ResponseWriter, r *http.Request) {
		_ = app.ForceKillZapret()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/app/force-exit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "message": "Приложение и все зависимости завершаются..."})
		_ = app.ForceExitAll()
	})

	mux.HandleFunc("/api/zapret/reset-drivers", func(w http.ResponseWriter, r *http.Request) {
		_ = app.ResetDrivers()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/benchmark", func(w http.ResponseWriter, r *http.Request) {
		stratID := r.URL.Query().Get("strategy")
		method := r.URL.Query().Get("method")
		targets := r.URL.Query().Get("targets")
		res := app.RunStrategyBenchmark(stratID, method, targets)
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/zapret/benchmark-all", func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Query().Get("method")
		targets := r.URL.Query().Get("targets")
		err := app.StartAutoBenchmark(method, targets)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "started"})
	})

	mux.HandleFunc("/api/zapret/benchmark-all/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.GetAutoBenchmarkStatus())
	})

	mux.HandleFunc("/api/zapret/targets", func(w http.ResponseWriter, r *http.Request) {
		group := r.URL.Query().Get("group")
		writeJSON(w, app.GetTargetsByGroup(group))
	})

	mux.HandleFunc("/api/zapret/open-test-log", func(w http.ResponseWriter, r *http.Request) {
		err := app.OpenTestLog()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/zapret/test-log", func(w http.ResponseWriter, r *http.Request) {
		content, err := app.GetTestLog()
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"content": content})
	})

	mux.HandleFunc("/api/zapret/ipset-mode", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mode := r.URL.Query().Get("mode")
			_ = app.SetIPSetMode(mode)
		}
		writeJSON(w, map[string]string{"mode": app.GetIPSetMode()})
	})

	mux.HandleFunc("/api/tg/start", func(w http.ResponseWriter, r *http.Request) {
		err := app.StartTGProxy()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/tg/stop", func(w http.ResponseWriter, r *http.Request) {
		_ = app.StopTGProxy()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/tg/ping", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.PingTelegramDCs())
	})

	mux.HandleFunc("/api/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.GetDiagnostics())
	})

	mux.HandleFunc("/api/platform/restart-admin", func(w http.ResponseWriter, r *http.Request) {
		err := app.RestartAsAdmin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/platform/fix-tcp", func(w http.ResponseWriter, r *http.Request) {
		err := app.FixTCPTimestamps()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/updater/check", func(w http.ResponseWriter, r *http.Request) {
		res, err := app.CheckAppUpdate()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	})

	mux.HandleFunc("/api/updater/assets", func(w http.ResponseWriter, r *http.Request) {
		res, err := app.UpdateAssets()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = app.SaveList(fn, body.Content)
		}
		content, _ := app.ReadList(fn)
		writeJSON(w, map[string]string{"file": fn, "content": content})
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"lines": app.GetLogs()})
	})

	mux.HandleFunc("/api/logs/clear", func(w http.ResponseWriter, r *http.Request) {
		app.ClearLogs()
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/debug-report", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"report": app.GenerateDebugReport()})
	})

	mux.HandleFunc("/api/open-url", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL != "" {
			openBrowser(targetURL)
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})
}
