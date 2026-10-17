package main

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
)

// securityMiddleware protects the local API from DNS rebinding, CSRF from web browsers,
// and unauthorized access by validating Host, Origin, Sec-Fetch-Site, security headers, and the session token.
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

		// 2. Origin Validation (prevents cross-origin requests from web browsers, including sandboxed/null origins)
		origin := r.Header.Get("Origin")
		if origin != "" && origin != expectedOrigin1 && origin != expectedOrigin2 {
			http.Error(w, "Forbidden: Cross-origin request not allowed", http.StatusForbidden)
			return
		}

		// 3. Sec-Fetch-Site Validation (allow empty, same-origin, none; block cross-site)
		secFetchSite := r.Header.Get("Sec-Fetch-Site")
		if secFetchSite != "" && secFetchSite != "same-origin" && secFetchSite != "none" {
			http.Error(w, "Forbidden: Cross-site request blocked", http.StatusForbidden)
			return
		}

		// 4. Security Headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'")

		// 5. Session Token Validation (Fail closed: required for everything except GET static assets)
		isStaticGET := r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/")
		if !isStaticGET {
			clientToken := r.Header.Get("X-Zapretify-Token")
			if subtle.ConstantTimeCompare([]byte(clientToken), []byte(token)) != 1 {
				http.Error(w, "Unauthorized: Invalid or missing API token", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
