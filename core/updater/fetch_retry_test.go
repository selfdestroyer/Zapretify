package updater

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A request that times out once is repeated; an HTTP error is not.
func TestFetchRetry(t *testing.T) {
	old := fetchRetryDelay
	fetchRetryDelay = 0
	defer func() { fetchRetryDelay = old }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/404" {
			atomic.AddInt32(&calls, 1)
			http.NotFound(w, r)
			return
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			time.Sleep(300 * time.Millisecond) // longer than the client timeout
		}
		_, _ = w.Write([]byte("1.2.3.0/24\n"))
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 100 * time.Millisecond}

	body, err := fetchRetry(client, srv.URL+"/list", 1024)
	if err != nil || string(body) != "1.2.3.0/24\n" || atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("body %q, err %v, calls %d", body, err, calls)
	}
	atomic.StoreInt32(&calls, 0)
	if _, err := fetchRetry(client, srv.URL+"/404", 1024); err == nil || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("404 must fail without a retry: err %v, calls %d", err, calls)
	}
}
