package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"zapretify/core/config"
	"zapretify/core/tgproxy"
	"zapretify/core/zapret"
)

func TestValidators(t *testing.T) {
	// Method validator
	if !validBenchmarkMethod("") || !validBenchmarkMethod("all") || !validBenchmarkMethod("dpi") || !validBenchmarkMethod("https") {
		t.Errorf("expected valid methods to pass")
	}
	if validBenchmarkMethod("curl; rm -rf /") || validBenchmarkMethod("unknown") {
		t.Errorf("expected malicious/unknown methods to fail")
	}

	// Target group validator
	if !validBenchmarkTargetGroup("") || !validBenchmarkTargetGroup("all") || !validBenchmarkTargetGroup("yt_discord") || !validBenchmarkTargetGroup("blockcheck") {
		t.Errorf("expected valid target groups to pass")
	}
	if validBenchmarkTargetGroup("malicious_group") {
		t.Errorf("expected unknown target group to fail")
	}

	// IPSet mode validator
	if !validIPSetMode("none") || !validIPSetMode("loaded") || !validIPSetMode("any") {
		t.Errorf("expected valid ipset modes to pass")
	}
	if validIPSetMode("custom") || validIPSetMode("inject") {
		t.Errorf("expected invalid ipset modes to fail")
	}

	// Allowed URL validator
	allowedURLs := []string{
		"https://github.com/selfdestroyer/Zapretify",
		"https://api.github.com/repos/selfdestroyer/Zapretify/releases/latest",
		"https://raw.githubusercontent.com/selfdestroyer/Zapretify/main/version.json",
		"https://t.me/zapretify",
		"tg://proxy?server=127.0.0.1&port=1443&secret=123",
		"tg://resolve?domain=zapretify",
		"tg://join?invite=abc",
	}
	for _, uStr := range allowedURLs {
		parsed, err := url.Parse(uStr)
		if err != nil || !isAllowedURL(parsed) {
			t.Errorf("expected %q to be allowed, got err=%v", uStr, err)
		}
	}

	blockedURLs := []string{
		"file:///C:/Windows/System32/cmd.exe",
		"cmd.exe",
		"powershell://invoke",
		"https://evil.com/payload.exe",
		"http://localhost:8080/exploit",
		"javascript:alert(1)",
		"data:text/html,<h1>test</h1>",
		"tg://",
		"tg://unknown",
		"tg://evil?command=run",
	}
	for _, uStr := range blockedURLs {
		parsed, err := url.Parse(uStr)
		if err == nil && isAllowedURL(parsed) {
			t.Errorf("expected %q to be blocked", uStr)
		}
	}
}

func setupTestAppAndMux() (*App, *http.ServeMux) {
	cfg := config.Get().Clone()
	runner := zapret.GetRunner()
	registry := zapret.GetRegistry()
	tgServer := tgproxy.GetServer()

	app := NewAppWith(cfg, runner, registry, tgServer)
	mux := http.NewServeMux()
	registerAPIRoutes(mux, app)
	return app, mux
}

func TestAPIMethodNotAllowed(t *testing.T) {
	_, mux := setupTestAppAndMux()

	postOnlyRoutes := []string{
		"/api/zapret/start",
		"/api/zapret/stop",
		"/api/zapret/install-service",
		"/api/zapret/remove-service",
		"/api/zapret/force-kill",
		"/api/app/force-exit",
		"/api/zapret/reset-drivers",
		"/api/zapret/benchmark",
		"/api/zapret/benchmark-all",
		"/api/zapret/open-test-log",
		"/api/tg/start",
		"/api/tg/stop",
		"/api/platform/restart-admin",
		"/api/platform/fix-tcp",
		"/api/updater/assets",
		"/api/logs/clear",
		"/api/open-url",
	}

	for _, route := range postOnlyRoutes {
		t.Run("GET disallowed on "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, route, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 for GET %s, got %d", route, rec.Code)
			}
		})
	}

	getOnlyRoutes := []string{
		"/api/status",
		"/api/zapret/strategies",
		"/api/zapret/benchmark/status",
		"/api/zapret/benchmark-all/status",
		"/api/zapret/targets",
		"/api/zapret/test-log",
		"/api/tg/ping",
		"/api/diagnostics",
		"/api/updater/check",
		"/api/logs",
		"/api/debug-report",
	}

	for _, route := range getOnlyRoutes {
		t.Run("POST disallowed on "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, route, strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 for POST %s, got %d", route, rec.Code)
			}
		})
	}

	dualRoutes := []string{
		"/api/config",
		"/api/zapret/ipset-mode",
		"/api/lists",
	}

	for _, route := range dualRoutes {
		t.Run("DELETE disallowed on "+route, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, route, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("expected 405 for DELETE %s, got %d", route, rec.Code)
			}
		})
	}
}

func TestAPIConfigHandling(t *testing.T) {
	app, mux := setupTestAppAndMux()

	// 1. GET /api/config returns 200 and valid JSON
	t.Run("GET config", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var cfg config.Config
		if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
	})

	// 2. Reject body > 1MB
	t.Run("POST config > 1MB rejected", func(t *testing.T) {
		largeData := bytes.Repeat([]byte("a"), 1024*1024+100)
		req := httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(largeData))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for >1MB body, got %d", rec.Code)
		}
	})

	// 3. Reject unknown fields
	t.Run("POST config with unknown fields rejected", func(t *testing.T) {
		payload := `{"unknown_field": "exploit", "app": {"language": "ru"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for unknown fields, got %d", rec.Code)
		}
	})

	// 4. Accept partial config update
	t.Run("POST partial config succeeds and merges", func(t *testing.T) {
		origLang := app.GetConfig().App.Language
		targetLang := "en"
		if origLang == "en" {
			targetLang = "ru"
		}
		payload := `{"app": {"language": "` + targetLang + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for partial update, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		if app.GetConfig().App.Language != targetLang {
			t.Errorf("expected language %s, got %s", targetLang, app.GetConfig().App.Language)
		}
	})

	// 5. Reject invalid config (e.g., bad port range)
	t.Run("POST invalid config validation rejected", func(t *testing.T) {
		payload := `{"zapret": {"game_filter_tcp": "invalid-port-abc"}}`
		req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid port range, got %d", rec.Code)
		}
	})
}

func TestAPISingleBenchmarkAsync(t *testing.T) {
	app, mux := setupTestAppAndMux()

	// Initial status should not be running
	st := app.GetSingleBenchmarkStatus()
	if st.Running {
		t.Errorf("benchmark should not be running initially")
	}

	// GET /api/zapret/benchmark/status returns 200
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/zapret/benchmark/status", nil)
	recStatus := httptest.NewRecorder()
	mux.ServeHTTP(recStatus, reqStatus)
	if recStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 for benchmark status, got %d", recStatus.Code)
	}

	// POST /api/zapret/benchmark starts benchmark asynchronously
	reqStart := httptest.NewRequest(http.MethodPost, "/api/zapret/benchmark?strategy=general&method=all&targets=all", nil)
	recStart := httptest.NewRecorder()
	mux.ServeHTTP(recStart, reqStart)
	if recStart.Code != http.StatusOK {
		t.Fatalf("expected 200 for benchmark start, got %d: %s", recStart.Code, recStart.Body.String())
	}
	var res map[string]string
	if err := json.NewDecoder(recStart.Body).Decode(&res); err != nil || res["status"] != "started" {
		t.Fatalf("expected status: started, got %v", res)
	}

	// While running, repeated start should return 400
	app.benchMu.Lock()
	app.benchStatus.Running = true
	app.benchMu.Unlock()

	reqSecond := httptest.NewRequest(http.MethodPost, "/api/zapret/benchmark?strategy=general", nil)
	recSecond := httptest.NewRecorder()
	mux.ServeHTTP(recSecond, reqSecond)
	if recSecond.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when benchmark already running, got %d", recSecond.Code)
	}

	// Reset benchmark status
	app.benchMu.Lock()
	app.benchStatus.Running = false
	app.benchMu.Unlock()
}

func TestAPIListsHandling(t *testing.T) {
	_, mux := setupTestAppAndMux()

	// POST body > 5MB to /api/lists should fail
	largeList := `{"content":"` + strings.Repeat("x", 5*1024*1024+100) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/lists?file=list-general.txt", strings.NewReader(largeList))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for lists body > 5MB, got %d", rec.Code)
	}
}
