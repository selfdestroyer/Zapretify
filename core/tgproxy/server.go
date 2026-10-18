package tgproxy

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"zapretify/core/logger"
)

type Server struct {
	host           string
	port           int
	secret         string
	cfproxy        bool
	h2             bool
	cfWorkers      []string
	cfUserDomains  []string
	forceTestDC    bool

	listener    net.Listener
	running     bool
	mu          sync.Mutex
	activeConns sync.Map
	ctx         context.Context
	cancel      context.CancelFunc
}

var (
	instance *Server
	once     sync.Once
)

func GetServer() *Server {
	once.Do(func() {
		instance = &Server{}
	})
	return instance
}

func (s *Server) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Server) Start(host string, port int, secret string, cfproxy bool, h2 bool, cfWorkers []string, cfUserDomains []string, forceTestDC bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 1443
	}

	// Clean secret (strip dd/ee only if string has length 34 = 2-byte prefix + 32 hex chars)
	cleanSecret := strings.TrimSpace(secret)
	lowerSecret := strings.ToLower(cleanSecret)
	if len(cleanSecret) == 34 && (strings.HasPrefix(lowerSecret, "dd") || strings.HasPrefix(lowerSecret, "ee")) {
		cleanSecret = cleanSecret[2:]
	}
	if len(cleanSecret) > 32 {
		cleanSecret = cleanSecret[:32]
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.host = host
	s.port = port
	s.secret = cleanSecret
	s.cfproxy = cfproxy
	s.h2 = h2
	s.cfWorkers = cfWorkers
	s.cfUserDomains = cfUserDomains
	s.forceTestDC = forceTestDC
	s.listener = l
	s.running = true
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Build runtime Config
	dcMap := make(map[int]string)
	dcPool := make(map[int][]string)
	for dc, ip := range dcFallbackDefaults {
		dcMap[dc] = ip
		dcPool[dc] = []string{ip}
	}

	cfDomains := defaultCFProxyDomains()
	if len(cfUserDomains) > 0 {
		cfDomains = appendUniqueDomains(cfDomains, cfUserDomains...)
	}

	cfg := &Config{
		Host:                         host,
		Port:                         port,
		SecretHex:                    cleanSecret,
		DCMap:                        dcMap,
		DCPool:                       dcPool,
		FallbackCFProxy:              cfproxy,
		FallbackCFProxyPriority:      true,
		FallbackCFProxyDomains:       cfDomains,
		FallbackCFProxyWorkerDomains: cfWorkers,
		BufKB:                        256,
		PoolSize:                     4,
		MaxConns:                     defaultMaxConns,
		ForceTestDC:                  forceTestDC,
	}

	var secretBytes []byte
	if cleanSecret != "" {
		secretBytes, _ = hex.DecodeString(cleanSecret)
	}

	maskedSecret := cleanSecret
	if len(maskedSecret) > 6 {
		maskedSecret = maskedSecret[:6] + "***"
	}
	logger.Log("[tg-ws-proxy] Сервер запущен на %s:%d (Secret: %s, CFProxy: %v)", host, port, maskedSecret, cfproxy)

	go s.acceptLoop(l, cfg, secretBytes)
	return nil
}

func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	s.running = false
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}

	// Force close all active client sessions immediately
	s.activeConns.Range(func(key, _ interface{}) bool {
		if conn, ok := key.(net.Conn); ok {
			_ = conn.Close()
		}
		s.activeConns.Delete(key)
		return true
	})

	globalPool.reset()
	logger.Log("[tg-ws-proxy] Сервер остановлен")
	return nil
}

func (s *Server) acceptLoop(l net.Listener, cfg *Config, secret []byte) {
	sessionsSem := make(chan struct{}, cfg.MaxConns)
	acceptBackoff := acceptBackoffMin
	tcpLn, _ := l.(*net.TCPListener)

	for {
		if tcpLn != nil {
			_ = tcpLn.SetDeadline(time.Now().Add(acceptPollTimeout))
		}
		c, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()
			if !running {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				acceptBackoff = acceptBackoffMin
				continue
			}
			time.Sleep(acceptBackoff)
			acceptBackoff *= 2
			if acceptBackoff > acceptBackoffMax {
				acceptBackoff = acceptBackoffMax
			}
			continue
		}
		acceptBackoff = acceptBackoffMin
		atomic.AddInt64(&stats.connectionsTotal, 1)

		s.activeConns.Store(c, struct{}{})

		select {
		case sessionsSem <- struct{}{}:
			go func(conn net.Conn) {
				defer func() { <-sessionsSem }()
				defer s.activeConns.Delete(conn)
				defer func() {
					if r := recover(); r != nil {
						_ = conn.Close()
						logger.Log("[tg-ws-proxy] [%s] panic recovered: %v", conn.RemoteAddr(), r)
					}
				}()
				handleClient(conn, cfg, secret)
			}(c)
		default:
			s.activeConns.Delete(c)
			logger.Log("[tg-ws-proxy] Достигнут лимит сессий (%d), соединение от %s отклонено", cfg.MaxConns, c.RemoteAddr())
			_ = c.Close()
		}
	}
}

func handleClient(client net.Conn, cfg *Config, secret []byte) {
	atomic.AddInt64(&stats.connectionsActive, 1)
	defer atomic.AddInt64(&stats.connectionsActive, -1)
	defer client.Close()
	label := client.RemoteAddr().String()

	_ = setSockOpts(client, cfg.BufKB*1024)

	// Peek first byte to see if this is TLS Handshake (Fake-TLS) or direct MTProto
	_ = client.SetReadDeadline(time.Now().Add(clientHandshakeTimeout))
	first := make([]byte, 1)
	if _, err := io.ReadFull(client, first); err != nil {
		return
	}

	if first[0] == tlsRecordHandshake {
		// Put back the first byte by creating a pre-buffered connection
		fconn, hs, ok := acceptFakeTLSClientWithFirstByte(client, first[0], secret, cfg.FakeTLSDomain, label)
		if !ok {
			return
		}
		hi, ok := tryHandshake(hs, secret)
		if !ok {
			atomic.AddInt64(&stats.connectionsBad, 1)
			return
		}
		handleMTProtoClient(fconn, cfg, hi, secret, label)
		return
	}

	// Direct MTProto obfuscated handshake
	hs := make([]byte, handshakeLen)
	hs[0] = first[0]
	if _, err := io.ReadFull(client, hs[1:]); err != nil {
		return
	}
	_ = client.SetReadDeadline(time.Time{})

	hi, ok := tryHandshake(hs, secret)
	if !ok {
		atomic.AddInt64(&stats.connectionsBad, 1)
		return
	}
	handleMTProtoClient(client, cfg, hi, secret, label)
}

func acceptFakeTLSClientWithFirstByte(client net.Conn, firstByte byte, secret []byte, maskingDomain string, label string) (net.Conn, []byte, bool) {
	tlsHeader := make([]byte, 5)
	tlsHeader[0] = firstByte
	if _, err := io.ReadFull(client, tlsHeader[1:]); err != nil {
		return nil, nil, false
	}
	recordLen := int(binaryBigEndianUint16(tlsHeader[3:5]))
	if recordLen <= 0 || recordLen > 64*1024 {
		return nil, nil, false
	}

	recordBody := make([]byte, recordLen)
	if _, err := io.ReadFull(client, recordBody); err != nil {
		return nil, nil, false
	}
	clientHello := append(tlsHeader, recordBody...)

	clientRandom, sessionID, ok := verifyFakeTLSClientHello(clientHello, secret)
	if !ok {
		if maskingDomain != "" {
			proxyToMaskingDomain(client, clientHello, maskingDomain, label)
		}
		return nil, nil, false
	}

	serverHello, err := buildFakeTLSServerHello(secret, clientRandom, sessionID)
	if err != nil {
		return nil, nil, false
	}
	_ = client.SetWriteDeadline(time.Now().Add(clientHandshakeTimeout))
	if _, err := client.Write(serverHello); err != nil {
		return nil, nil, false
	}
	_ = client.SetWriteDeadline(time.Time{})

	wrapped := &fakeTLSConn{raw: client}
	hs := make([]byte, handshakeLen)
	_ = wrapped.SetReadDeadline(time.Now().Add(clientHandshakeTimeout))
	if _, err := io.ReadFull(wrapped, hs); err != nil {
		return nil, nil, false
	}
	_ = wrapped.SetReadDeadline(time.Time{})
	return wrapped, hs, true
}

func binaryBigEndianUint16(b []byte) uint16 {
	_ = b[1]
	return uint16(b[1]) | uint16(b[0])<<8
}

func splitWSTargets(targets []string, skipCooldown bool) (directTargets, frontingTargets []string) {
	frontingTargets = targets
	if !skipCooldown {
		return targets, frontingTargets
	}
	directTargets = make([]string, 0, len(targets))
	for _, target := range targets {
		if !inIPCooldown(target) {
			directTargets = append(directTargets, target)
		}
	}
	return directTargets, frontingTargets
}

func handleMTProtoClient(client net.Conn, cfg *Config, hi *handshakeInfo, secret []byte, label string) {
	protoInt := protoFromTag(hi.ProtoTag)
	mediaTag := ""
	if hi.IsMedia {
		mediaTag = " media"
	}

	relayInit := generateRelayInit(hi.ProtoTag, signedDC(hi.DC, hi.IsMedia))
	cltDec, cltEnc, tgEnc, tgDec, err := buildCiphers(hi.ClientDecI, relayInit, secret)
	if err != nil {
		logger.Log("[tg-ws-proxy] [%s] ошибка инициализации шифра: %v", label, err)
		return
	}

	newFallbackSplitter := func() *msgSplitter {
		ms, err := newMsgSplitter(relayInit, protoInt)
		if err != nil {
			return nil
		}
		return ms
	}

	doFallback := func(setState bool, wsFailedRedirect bool, allRedirect bool, primaryTarget string) {
		key := dcKey{DC: hi.DC, IsMedia: hi.IsMedia}
		if setState {
			if wsFailedRedirect && allRedirect {
				setBlacklisted(key)
			} else {
				setCooldown(key)
			}
		}

		fallback := fallbackIP(hi.DC)
		if fallback == "" {
			fallback = primaryTarget
		}

		useWorker := cfg.hasCFProxyWorkerDomains()
		tryWorker := func() bool {
			if err := cfWorkerFallback(label, cfg, hi.DC, hi.IsMedia, fallback, client, relayInit, cltDec, cltEnc, tgEnc, tgDec, nil); err == nil {
				return true
			}
			return false
		}

		useCF := cfg.FallbackCFProxy && cfg.hasCFProxyDomains()
		tryCF := func() bool {
			splitter := newFallbackSplitter()
			if err := cfproxyFallback(label, cfg, hi.DC, hi.IsMedia, client, relayInit, cltDec, cltEnc, tgEnc, tgDec, splitter); err == nil {
				return true
			}
			return false
		}
		tryTCP := func() bool {
			if fallback == "" {
				return false
			}
			logger.Log("[tg-ws-proxy] [%s] DC%d%s -> TCP fallback к %s:443", label, hi.DC, mediaTag, fallback)
			err := tcpFallback(client, fallback, relayInit, cltDec, cltEnc, tgEnc, tgDec)
			return err == nil
		}

		if useWorker && tryWorker() {
			return
		}

		if useCF && cfg.FallbackCFProxyPriority {
			if tryCF() || tryTCP() {
				return
			}
		} else if useCF {
			if tryTCP() || tryCF() {
				return
			}
		} else if tryTCP() {
			return
		}

		logger.Log("[tg-ws-proxy] [%s] DC%d%s нет доступных путей fallback", label, hi.DC, mediaTag)
	}

	if isBlacklisted(hi.DC, hi.IsMedia) {
		doFallback(false, false, false, "")
		return
	}

	targets, hasTarget := cfg.DCPool[hi.DC]
	if !hasTarget || len(targets) == 0 {
		doFallback(false, false, false, "")
		return
	}
	primaryTarget := targets[0]
	hasAnyCFFallback := cfg.hasCFProxyWorkerDomains() || (cfg.FallbackCFProxy && cfg.hasCFProxyDomains())
	directTargets, frontingTargets := splitWSTargets(targets, hasAnyCFFallback)
	wsTarget := primaryTarget
	if len(directTargets) > 0 {
		wsTarget = directTargets[0]
	}

	dcW := hi.DC
	if v, ok := dcOverrides[dcW]; ok {
		dcW = v
	}
	domains := wsDomains(dcW, hi.IsMedia)
	key := dcKey{DC: hi.DC, IsMedia: hi.IsMedia}

	connectWS := func(timeout time.Duration) (*websocket.Conn, string, bool, bool, bool) {
		wsFailedRedirect := false
		allRedirect := true
		timedOut := false
		for _, target := range directTargets {
			for _, d := range domains {
				conn, resp, err := dialWS(target, d, timeout)
				if err == nil {
					return conn, target, false, false, false
				}
				if isTimeoutError(err) {
					timedOut = true
				}
				if resp != nil && isRedirect(resp.StatusCode) {
					wsFailedRedirect = true
				} else {
					allRedirect = false
				}
			}
		}
		return nil, "", wsFailedRedirect, allRedirect, timedOut
	}

	if inCooldown(key) {
		doFallback(false, false, false, primaryTarget)
		return
	}

	// Check pool
	if ws := globalPool.get(cfg, key, wsTarget, domains); ws != nil {
		atomic.AddInt64(&stats.connectionsWS, 1)
		splitter, _ := newMsgSplitter(relayInit, protoInt)
		if err := writeWSBinary(ws, relayInit); err == nil {
			bridgeWS(label, cfg, hi.DC, hi.IsMedia, client, ws, cltDec, cltEnc, tgEnc, tgDec, splitter)
			return
		}
		_ = ws.Close()
	}

	// Try fresh connect
	ws, connectedTarget, wsFailedRedirect, allRedirect, timedOut := connectWS(wsConnectTimeout)
	if ws != nil {
		clearCooldown(key)
		atomic.AddInt64(&stats.connectionsWS, 1)
		splitter, _ := newMsgSplitter(relayInit, protoInt)
		if err := writeWSBinary(ws, relayInit); err == nil {
			bridgeWS(label, cfg, hi.DC, hi.IsMedia, client, ws, cltDec, cltEnc, tgEnc, tgDec, splitter)
			return
		}
		_ = ws.Close()
	}

	if timedOut && len(directTargets) > 0 {
		setIPCooldown(directTargets[0])
	}

	// Try domain fronting
	if frontingActive() {
		for _, target := range frontingTargets {
			conn, _, ferr := wsConnectFronting(target, domains, wsConnectTimeout)
			if ferr == nil {
				atomic.AddInt64(&stats.connectionsFront, 1)
				splitter, _ := newMsgSplitter(relayInit, protoInt)
				if err := writeWSBinary(conn, relayInit); err == nil {
					bridgeWS(label, cfg, hi.DC, hi.IsMedia, client, conn, cltDec, cltEnc, tgEnc, tgDec, splitter)
					return
				}
				_ = conn.Close()
			}
		}
	}

	_ = connectedTarget
	doFallback(true, wsFailedRedirect, allRedirect, primaryTarget)
}

func (s *Server) GetProxyLink() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.host
	if h == "" || h == "0.0.0.0" {
		h = "127.0.0.1"
	}
	return fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=dd%s", h, s.port, s.secret)
}

func PingDC(ip string, port int, timeout time.Duration) (bool, float64) {
	start := time.Now()
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		return false, 0
	}
	_ = conn.Close()
	return true, elapsed
}
