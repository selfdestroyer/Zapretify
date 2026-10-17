package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityMiddleware(t *testing.T) {
	token := "secure123token"
	port := 8080

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	handler := securityMiddleware(token, port, okHandler)

	tests := []struct {
		name           string
		path           string
		host           string
		origin         string
		headerToken    string
		queryToken     string
		expectedStatus int
	}{
		{
			name:           "Valid API request with header token",
			path:           "/api/status",
			host:           "127.0.0.1:8080",
			origin:         "http://127.0.0.1:8080",
			headerToken:    token,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Valid API request with query token",
			path:           "/api/status",
			host:           "localhost:8080",
			origin:         "http://localhost:8080",
			queryToken:     token,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Static file request allowed without token",
			path:           "/index.html",
			host:           "127.0.0.1:8080",
			origin:         "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Invalid Host blocked (DNS rebinding)",
			path:           "/api/status",
			host:           "evil.attacker.com",
			headerToken:    token,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Invalid Origin blocked (CSRF)",
			path:           "/api/status",
			host:           "127.0.0.1:8080",
			origin:         "http://evil.attacker.com",
			headerToken:    token,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "Missing token on API path",
			path:           "/api/status",
			host:           "127.0.0.1:8080",
			origin:         "http://127.0.0.1:8080",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Wrong token on API path",
			path:           "/api/status",
			host:           "127.0.0.1:8080",
			origin:         "http://127.0.0.1:8080",
			headerToken:    "wrong-token",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := tt.path
			if tt.queryToken != "" {
				url += "?token=" + tt.queryToken
			}
			req := httptest.NewRequest("GET", url, nil)
			req.Host = tt.host
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.headerToken != "" {
				req.Header.Set("X-Zapretify-Token", tt.headerToken)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}
