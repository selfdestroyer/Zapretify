package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// securityMiddleware protects the local API from DNS rebinding, CSRF from web browsers,
// and unauthorized access by validating Host, Origin, and the session token.
func securityMiddleware(token string, port int, next http.Handler) http.Handler {
	expectedHost1 := fmt.Sprintf("127.0.0.1:%d", port)
	expectedHost2 := fmt.Sprintf("localhost:%d", port)
	expectedOrigin1 := fmt.Sprintf("http://127.0.0.1:%d", port)
	expectedOrigin2 := fmt.Sprintf("http://localhost:%d", port)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host Validation (prevents DNS rebinding attacks)
		host := r.Host
		if host != expectedHost1 && host != expectedHost2 && host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "Forbidden: Invalid Host header", http.StatusForbidden)
			return
		}

		// 2. Origin Validation (prevents cross-origin requests from web browsers)
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "null" {
			if origin != expectedOrigin1 && origin != expectedOrigin2 {
				http.Error(w, "Forbidden: Cross-origin request not allowed", http.StatusForbidden)
				return
			}
		}

		// 3. Session Token Validation for all /api/* endpoints
		if strings.HasPrefix(r.URL.Path, "/api/") {
			clientToken := r.Header.Get("X-Zapretify-Token")
			if clientToken == "" {
				clientToken = r.URL.Query().Get("token")
			}

			if subtle.ConstantTimeCompare([]byte(clientToken), []byte(token)) != 1 {
				http.Error(w, "Unauthorized: Invalid or missing API token", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
