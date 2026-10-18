package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const sessionCookieName = "zapretify_setup"

// newSessionToken returns a random per-run token.
func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// guard protects the installer's local HTTP server. The installer runs elevated, so any web page the
// user happens to have open must not be able to drive it:
//   - the Host header must be the loopback address with our port (blocks DNS rebinding);
//   - the page itself (static files) receives a random HttpOnly SameSite=Strict cookie;
//   - every /api/ call must present that cookie, come from our own origin, and be a same-origin fetch.
func guard(token string, port int, next http.Handler) http.Handler {
	allowedHosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", port): true,
		fmt.Sprintf("localhost:%d", port): true,
	}
	allowedOrigins := map[string]bool{
		fmt.Sprintf("http://127.0.0.1:%d", port): true,
		fmt.Sprintf("http://localhost:%d", port): true,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHosts[strings.ToLower(r.Host)] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookieName,
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" && !allowedOrigins[strings.ToLower(origin)] {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err != nil || !allowedHosts[strings.ToLower(u.Host)] {
				http.Error(w, "forbidden referer", http.StatusForbidden)
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "forbidden fetch site", http.StatusForbidden)
			return
		}

		c, err := r.Cookie(sessionCookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
