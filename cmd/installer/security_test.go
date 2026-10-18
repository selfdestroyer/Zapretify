package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newGuarded(t *testing.T) (http.Handler, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("pong")) })
	mux.HandleFunc("/index.html", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>")) })
	return guard("secret-token", 5555, mux), "secret-token"
}

func do(h http.Handler, method, path, host string, hdr map[string]string, withCookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if withCookie {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "secret-token"})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGuardPageSetsCookie(t *testing.T) {
	h, _ := newGuarded(t)
	rec := do(h, "GET", "/index.html", "127.0.0.1:5555", nil, false)
	if rec.Code != 200 {
		t.Fatalf("page: %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie not set correctly: %+v", cookies)
	}
}

func TestGuardAPI(t *testing.T) {
	h, _ := newGuarded(t)
	good := map[string]string{"Origin": "http://127.0.0.1:5555", "Sec-Fetch-Site": "same-origin"}

	cases := []struct {
		name   string
		host   string
		hdr    map[string]string
		cookie bool
		want   int
	}{
		{"ok", "127.0.0.1:5555", good, true, 200},
		{"ok without browser headers", "localhost:5555", nil, true, 200},
		{"no cookie", "127.0.0.1:5555", good, false, 401},
		{"foreign host (DNS rebinding)", "evil.example:5555", good, true, 403},
		{"foreign origin", "127.0.0.1:5555", map[string]string{"Origin": "https://evil.example"}, true, 403},
		{"cross-site fetch", "127.0.0.1:5555", map[string]string{"Sec-Fetch-Site": "cross-site"}, true, 403},
		{"foreign referer", "127.0.0.1:5555", map[string]string{"Referer": "https://evil.example/x"}, true, 403},
		{"wrong port", "127.0.0.1:6666", good, true, 403},
	}
	for _, c := range cases {
		if got := do(h, "POST", "/api/ping", c.host, c.hdr, c.cookie).Code; got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestGuardRejectsWrongToken(t *testing.T) {
	h, _ := newGuarded(t)
	req := httptest.NewRequest("GET", "http://127.0.0.1:5555/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "other"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("wrong token must be rejected, got %d", rec.Code)
	}
}
