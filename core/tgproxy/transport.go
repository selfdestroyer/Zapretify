package tgproxy

import (
	"context"
	"crypto/cipher"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"zapretify/core/logger"
)

var (
	errNoDomains = errors.New("no domains to connect")

	ioBufPool = sync.Pool{
		New: func() any {
			return make([]byte, 64*1024)
		},
	}
)

func dialWS(targetIP, domain string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	return dialWSWithSNI(targetIP, domain, domain, timeout)
}

func dialWSFronting(targetIP, domain string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	return dialWSWithSNI(targetIP, domain, "sprinthost.ru", timeout)
}

func dialWSWithSNI(targetIP, domain, sni string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	u := url.URL{Scheme: "wss", Host: domain, Path: "/apiws"}
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		Subprotocols:     []string{"binary"},
		TLSClientConfig: &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return newUpstreamDialer(timeout).DialContext(ctx, "tcp", net.JoinHostPort(targetIP, "443"))
		},
	}
	headers := http.Header{}
	headers.Set("Host", domain)
	headers.Set("Origin", "https://web.telegram.org")
	return dialer.Dial(u.String(), headers)
}

func writeWSBinary(ws *websocket.Conn, data []byte) error {
	if err := ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	err := ws.WriteMessage(websocket.BinaryMessage, data)
	if err == nil {
		_ = ws.SetWriteDeadline(time.Time{})
	}
	return err
}

func writeTCPRelayInit(conn net.Conn, relayInit []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	_, err := conn.Write(relayInit)
	if err == nil {
		_ = conn.SetWriteDeadline(time.Time{})
	}
	return err
}

func runIdleWatchdog(lastActivity *atomic.Int64, stop <-chan struct{}, timeout, checkInterval time.Duration, onIdle func()) {
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if time.Now().UnixNano()-lastActivity.Load() >= timeout.Nanoseconds() {
				onIdle()
				return
			}
		}
	}
}

func startIdleWatchdog(lastActivity *atomic.Int64, stop <-chan struct{}, onIdle func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		runIdleWatchdog(lastActivity, stop, ioIdleTimeout, idleWatchdogCheckInterval, onIdle)
	}()
	return done
}

func bridgeWS(label string, cfg *Config, dc int, isMedia bool, client net.Conn, ws *websocket.Conn, cltDec, cltEnc, tgEnc, tgDec cipher.Stream, splitter *msgSplitter) {
	done := make(chan struct{}, 2)
	stopIdleWatchdog := make(chan struct{})
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	idleWatchdogDone := startIdleWatchdog(&lastActivity, stopIdleWatchdog, func() {
		_ = ws.Close()
		_ = client.Close()
	})

	go func() {
		defer func() { done <- struct{}{} }()
		buf := ioBufPool.Get().([]byte)
		defer ioBufPool.Put(buf)
		var upPending int64
		defer func() {
			if upPending > 0 {
				atomic.AddInt64(&stats.bytesUp, upPending)
			}
		}()
		for {
			n, err := client.Read(buf)
			if n > 0 {
				lastActivity.Store(time.Now().UnixNano())
				upPending += int64(n)
				if upPending >= statsFlushBytes {
					atomic.AddInt64(&stats.bytesUp, upPending)
					upPending = 0
				}
				chunk := buf[:n]
				cltDec.XORKeyStream(chunk, chunk)
				tgEnc.XORKeyStream(chunk, chunk)

				if splitter != nil {
					parts := splitter.split(chunk)
					if len(parts) == 0 {
						continue
					}
					for _, p := range parts {
						_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
						if werr := ws.WriteMessage(websocket.BinaryMessage, p); werr != nil {
							return
						}
					}
				} else {
					_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
					if werr := ws.WriteMessage(websocket.BinaryMessage, chunk); werr != nil {
						return
					}
				}
			}
			if err != nil {
				if splitter != nil {
					for _, p := range splitter.flush() {
						_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
						_ = ws.WriteMessage(websocket.BinaryMessage, p)
					}
				}
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buf := ioBufPool.Get().([]byte)
		defer ioBufPool.Put(buf)
		var downPending int64
		defer func() {
			if downPending > 0 {
				atomic.AddInt64(&stats.bytesDown, downPending)
			}
		}()
		for {
			mt, r, err := ws.NextReader()
			if err != nil {
				return
			}
			if mt != websocket.BinaryMessage {
				continue
			}
			for {
				nr, rerr := r.Read(buf)
				if nr > 0 {
					lastActivity.Store(time.Now().UnixNano())
					n := int64(nr)
					downPending += n
					if downPending >= statsFlushBytes {
						atomic.AddInt64(&stats.bytesDown, downPending)
						downPending = 0
					}
					chunk := buf[:nr]
					tgDec.XORKeyStream(chunk, chunk)
					cltEnc.XORKeyStream(chunk, chunk)
					_ = client.SetWriteDeadline(time.Now().Add(ioIdleTimeout))
					if _, werr := client.Write(chunk); werr != nil {
						return
					}
				}
				if rerr != nil {
					if rerr == io.EOF {
						break
					}
					return
				}
			}
		}
	}()

	<-done
	close(stopIdleWatchdog)
	_ = ws.Close()
	_ = client.Close()
	<-done
	<-idleWatchdogDone
}

func tcpFallback(client net.Conn, dst string, relayInit []byte, cltDec, cltEnc, tgEnc, tgDec cipher.Stream) error {
	r, err := newUpstreamDialer(tcpDialTimeout).Dial("tcp", net.JoinHostPort(dst, "443"))
	if err != nil {
		logger.Log("[tg-ws-proxy] TCP fallback to %s:443 failed: %v", dst, err)
		return err
	}
	defer r.Close()
	atomic.AddInt64(&stats.connectionsTCP, 1)
	atomic.StoreInt64(&cfFailStreak, 0) // Telegram is reachable another way

	if err := writeTCPRelayInit(r, relayInit); err != nil {
		return err
	}

	done := make(chan struct{}, 2)
	stopIdleWatchdog := make(chan struct{})
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())
	idleWatchdogDone := startIdleWatchdog(&lastActivity, stopIdleWatchdog, func() {
		_ = client.Close()
		_ = r.Close()
	})

	go func() {
		defer func() { done <- struct{}{} }()
		buf := ioBufPool.Get().([]byte)
		defer ioBufPool.Put(buf)
		var upPending int64
		defer func() {
			if upPending > 0 {
				atomic.AddInt64(&stats.bytesUp, upPending)
			}
		}()
		for {
			n, err := client.Read(buf)
			if n > 0 {
				lastActivity.Store(time.Now().UnixNano())
				upPending += int64(n)
				if upPending >= statsFlushBytes {
					atomic.AddInt64(&stats.bytesUp, upPending)
					upPending = 0
				}
				chunk := buf[:n]
				cltDec.XORKeyStream(chunk, chunk)
				tgEnc.XORKeyStream(chunk, chunk)
				_ = r.SetWriteDeadline(time.Now().Add(ioIdleTimeout))
				if _, werr := r.Write(chunk); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buf := ioBufPool.Get().([]byte)
		defer ioBufPool.Put(buf)
		var downPending int64
		defer func() {
			if downPending > 0 {
				atomic.AddInt64(&stats.bytesDown, downPending)
			}
		}()
		for {
			n, err := r.Read(buf)
			if n > 0 {
				lastActivity.Store(time.Now().UnixNano())
				downPending += int64(n)
				if downPending >= statsFlushBytes {
					atomic.AddInt64(&stats.bytesDown, downPending)
					downPending = 0
				}
				chunk := buf[:n]
				tgDec.XORKeyStream(chunk, chunk)
				cltEnc.XORKeyStream(chunk, chunk)
				_ = client.SetWriteDeadline(time.Now().Add(ioIdleTimeout))
				if _, werr := client.Write(chunk); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	<-done
	close(stopIdleWatchdog)
	_ = client.Close()
	_ = r.Close()
	<-done
	<-idleWatchdogDone
	return nil
}

func wsConnect(targetIP string, domains []string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	return wsConnectWithDialer(targetIP, domains, timeout, dialWS)
}

func wsConnectFronting(targetIP string, domains []string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	return wsConnectWithDialer(targetIP, domains, timeout, dialWSFronting)
}

func wsConnectWithDialer(targetIP string, domains []string, timeout time.Duration, dial func(string, string, time.Duration) (*websocket.Conn, *http.Response, error)) (*websocket.Conn, *http.Response, error) {
	var lastErr error
	var lastResp *http.Response
	for _, domain := range domains {
		conn, resp, err := dial(targetIP, domain, timeout)
		if err == nil {
			return conn, resp, nil
		}
		lastErr = err
		lastResp = resp
	}
	if lastErr == nil {
		lastErr = errNoDomains
	}
	return nil, lastResp, lastErr
}

func dialWSByDomain(domain string, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	u := url.URL{Scheme: "wss", Host: domain, Path: "/apiws"}
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		Subprotocols:     []string{"binary"},
		TLSClientConfig: &tls.Config{
			ServerName:         domain,
			InsecureSkipVerify: true,
		},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return newUpstreamDialer(timeout).DialContext(ctx, network, addr)
		},
	}
	headers := http.Header{}
	headers.Set("Host", domain)
	headers.Set("Origin", "https://web.telegram.org")
	return dialer.Dial(u.String(), headers)
}

func dialWSWorker(worker, dst string, dc int, timeout time.Duration) (*websocket.Conn, *http.Response, error) {
	q := url.Values{}
	q.Set("dst", dst)
	q.Set("dc", strconv.Itoa(dc))
	u := url.URL{Scheme: "wss", Host: worker, Path: "/apiws", RawQuery: q.Encode()}
	dialer := websocket.Dialer{
		HandshakeTimeout: timeout,
		Subprotocols:     []string{"binary"},
		TLSClientConfig: &tls.Config{
			ServerName:         worker,
			InsecureSkipVerify: true,
		},
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return newUpstreamDialer(timeout).DialContext(ctx, network, addr)
		},
	}
	headers := http.Header{}
	headers.Set("Host", worker)
	headers.Set("Origin", "https://web.telegram.org")
	return dialer.Dial(u.String(), headers)
}

func cfWorkerFallback(label string, cfg *Config, dc int, isMedia bool, dst string, client net.Conn, relayInit []byte, cltDec, cltEnc, tgEnc, tgDec cipher.Stream, splitter *msgSplitter) error {
	mediaTag := ""
	if isMedia {
		mediaTag = " media"
	}
	if dst == "" {
		return errNoDomains
	}

	for _, worker := range cfg.cfproxyWorkerDomainsForTry() {
		logger.Log("[tg-ws-proxy] [%s] DC%d%s -> CF worker wss://%s/apiws?dst=%s", label, dc, mediaTag, worker, dst)
		ws, _, err := dialWSWorker(worker, dst, dc, wsConnectTimeout)
		if err != nil {
			atomic.AddInt64(&stats.wsErrors, 1)
			continue
		}

		if err := writeWSBinary(ws, relayInit); err != nil {
			_ = ws.Close()
			continue
		}

		atomic.AddInt64(&stats.connectionsCF, 1)
		bridgeWS(label, cfg, dc, isMedia, client, ws, cltDec, cltEnc, tgEnc, tgDec, splitter)
		return nil
	}

	return errNoDomains
}

// cfFailStreak counts CF proxy attempts that failed in a row. Some providers block the CF proxy
// domain by SNI and Telegram's own addresses as well; then the proxy cannot connect at all, and the
// UI suggests letting Zapret cover Cloudflare (IPSet «loaded») or adding the domain to a user list.
var cfFailStreak int64

const cfBlockedAfter = 4

func noteCFProxyFailure() {
	if atomic.AddInt64(&cfFailStreak, 1) == cfBlockedAfter {
		logger.Log("[tg-ws-proxy] Провайдер, похоже, блокирует путь через Cloudflare: включите Zapret с IPSet «loaded» или добавьте %s в свой список доменов", defaultCFProxyDomain)
	}
}

// CFProxyBlocked reports that the last CF proxy attempts all failed.
func CFProxyBlocked() bool { return atomic.LoadInt64(&cfFailStreak) >= cfBlockedAfter }

func cfproxyFallback(label string, cfg *Config, dc int, isMedia bool, client net.Conn, relayInit []byte, cltDec, cltEnc, tgEnc, tgDec cipher.Stream, splitter *msgSplitter) error {
	mediaTag := ""
	if isMedia {
		mediaTag = " media"
	}

	for _, baseDomain := range cfg.cfproxyDomainsForTry(dc) {
		domain := fmt.Sprintf("kws%d.%s", dc, baseDomain)
		logger.Log("[tg-ws-proxy] [%s] DC%d%s -> CF proxy wss://%s/apiws", label, dc, mediaTag, domain)
		ws, resp, err := dialWSByDomain(domain, wsConnectTimeout)
		if err != nil {
			atomic.AddInt64(&stats.wsErrors, 1)
			cfg.markCFProxyDomainFailed(baseDomain, cfProxyFailCooldown)
			_ = resp
			logger.Log("[tg-ws-proxy] [%s] CF proxy %s недоступен: %v", label, domain, err)
			noteCFProxyFailure()
			continue
		}

		if err := writeWSBinary(ws, relayInit); err != nil {
			logger.Log("[tg-ws-proxy] [%s] CF proxy %s: ошибка отправки: %v", label, domain, err)
			noteCFProxyFailure()
			_ = ws.Close()
			cfg.markCFProxyDomainFailed(baseDomain, cfProxyFailCooldown)
			continue
		}

		atomic.AddInt64(&stats.connectionsCF, 1)
		atomic.StoreInt64(&cfFailStreak, 0)
		cfg.promoteCFProxyDomain(dc, baseDomain)
		bridgeWS(label, cfg, dc, isMedia, client, ws, cltDec, cltEnc, tgEnc, tgDec, splitter)
		return nil
	}

	return errNoDomains
}
