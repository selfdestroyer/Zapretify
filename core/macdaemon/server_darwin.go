package macdaemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// maxRequestSize bounds a command: the largest real one is a strategy ID.
const maxRequestSize = 4 << 10

// Server is zapretifyd: it accepts commands on the socket and owns the engine.
type Server struct {
	OwnerUID int    // the user who installed the daemon; root is always allowed
	RootDir  string // RootDir in production
	Version  string

	mu       sync.Mutex // serialises commands and engine state
	proc     *engineProc
	tcpPorts []string // ports of the running strategy, reused on a network change
	udpPorts []string
	lastExit string
	note     string // warning about the current state (a VPN carries part of the traffic)
	state    bootState
	log      *ringLog
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(l net.Listener) error {
	if s.log == nil {
		s.log = &ringLog{}
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

// Listen creates the socket; access is checked per connection by peer uid, so the file is world-writable.
func Listen(path string) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0666); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Startup clears what a previous, crashed daemon left behind and, if the user asked for it,
// starts the engine with the last strategy (the bypass works right after the Mac boots).
func (s *Server) Startup() {
	if s.log == nil {
		s.log = &ringLog{}
	}
	cleanupStale(s.RootDir)
	s.state = loadBootState(s.RootDir)
	if s.state.Boot && s.state.Strategy != "" {
		go s.bootStart()
	}
}

// Shutdown stops the engine and removes its pf rules.
func (s *Server) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopEngine()
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	resp := Response{Protocol: ProtocolVersion, Version: s.Version}
	reply := func(err error) {
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
		}
		_ = json.NewEncoder(conn).Encode(resp)
	}

	uid, err := peerUID(conn)
	if err != nil {
		reply(err)
		return
	}
	if uid != 0 && uid != s.OwnerUID {
		reply(fmt.Errorf("команды принимаются только от пользователя, установившего Zapretify"))
		return
	}

	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, maxRequestSize)).Decode(&req); err != nil {
		reply(fmt.Errorf("неверный запрос: %w", err))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Cmd {
	case CmdStatus:
	case CmdStart:
		var home string
		var dataUID int
		if home, dataUID, err = userHome(uid, s.OwnerUID); err == nil {
			s.stopEngine()
			s.logf("запуск стратегии %q", req.Strategy)
			if err = s.startEngine(req.Strategy, home, dataUID); err != nil {
				s.logf("ошибка запуска: %v", err)
			} else if s.state.Strategy != req.Strategy {
				s.state.Strategy = req.Strategy
				s.saveState()
			}
		}
	case CmdStop:
		s.stopEngine()
	case CmdBoot:
		if req.Boot != nil {
			s.state.Boot = *req.Boot
		}
		if req.Strategy != "" {
			s.state.Strategy = req.Strategy
		}
		s.saveState()
	default:
		err = fmt.Errorf("неизвестная команда %q", req.Cmd)
	}
	s.fillStatus(&resp)
	reply(err)
}

func (s *Server) fillStatus(r *Response) {
	if s.proc != nil {
		r.Running = true
		r.Strategy = s.proc.strategy
		r.PID = s.proc.cmd.Process.Pid
		r.Iface = s.proc.iface
		r.IPv6 = s.proc.ipv6
	}
	r.LastExit = s.lastExit
	r.Note = s.note
	r.Boot = s.state.Boot
	r.Log = s.log.tail(maxLogLines)
}

func (s *Server) logf(format string, args ...any) { log.Printf(format, args...) }

// userHome is the home folder and uid of the user the daemon works for; root acts for the owner.
// The bases are read from UserDataSubdir inside it and must belong to that uid.
func userHome(peerUID, ownerUID int) (string, int, error) {
	uid := peerUID
	if uid == 0 {
		uid = ownerUID
	}
	u, err := user.LookupId(fmt.Sprint(uid))
	if err != nil {
		return "", 0, fmt.Errorf("пользователь %d: %w", uid, err)
	}
	if !filepath.IsAbs(u.HomeDir) {
		return "", 0, fmt.Errorf("пользователь %d: домашняя папка %q", uid, u.HomeDir)
	}
	return u.HomeDir, uid, nil
}

func peerUID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, errors.New("не unix-сокет")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return -1, err
	}
	if credErr != nil {
		return -1, fmt.Errorf("LOCAL_PEERCRED: %w", credErr)
	}
	return int(cred.Uid), nil
}
