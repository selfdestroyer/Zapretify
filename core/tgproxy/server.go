package tgproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Server struct {
	host      string
	port      int
	secret    string
	cfproxy   bool
	h2        bool
	cfWorkers []string

	listener net.Listener
	running  bool
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
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

func (s *Server) Start(host string, port int, secret string, cfproxy bool, h2 bool, cfWorkers []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.host = host
	s.port = port
	s.secret = secret
	s.cfproxy = cfproxy
	s.h2 = h2
	s.cfWorkers = cfWorkers
	s.listener = l
	s.running = true
	s.ctx, s.cancel = context.WithCancel(context.Background())

	go s.acceptLoop(l)
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
	return nil
}

func (s *Server) acceptLoop(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			s.mu.Lock()
			running := s.running
			s.mu.Unlock()
			if !running {
				return
			}
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *Server) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	// Upstream connection to Telegram DC or Cloudflare Worker
	// Default Telegram DC2 IP
	upstreamTarget := "149.154.167.220:443"
	
	dialer := &websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
	}

	// If CF Worker is configured, use it as WS bridge
	if s.cfproxy && len(s.cfWorkers) > 0 && s.cfWorkers[0] != "" {
		wsURL := fmt.Sprintf("wss://%s/api", s.cfWorkers[0])
		header := http.Header{}
		header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0 Safari/537.36")

		wsConn, _, err := dialer.Dial(wsURL, header)
		if err != nil {
			// Fallback to direct TCP
			s.fallbackDirect(clientConn, upstreamTarget)
			return
		}
		defer wsConn.Close()

		// Pipe between client TCP and upstream WebSocket
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			buf := make([]byte, 32*1024)
			for {
				n, err := clientConn.Read(buf)
				if err != nil {
					return
				}
				if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
		}()

		go func() {
			defer wg.Done()
			for {
				_, msg, err := wsConn.ReadMessage()
				if err != nil {
					return
				}
				if _, err := clientConn.Write(msg); err != nil {
					return
				}
			}
		}()

		wg.Wait()
		return
	}

	// Direct TCP Bridge fallback
	s.fallbackDirect(clientConn, upstreamTarget)
}

func (s *Server) fallbackDirect(clientConn net.Conn, target string) {
	remoteConn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer remoteConn.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	pipe := func(src, dst net.Conn) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}

	go pipe(clientConn, remoteConn)
	go pipe(remoteConn, clientConn)

	wg.Wait()
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
	addr := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		return false, 0
	}
	_ = conn.Close()
	return true, elapsed
}
