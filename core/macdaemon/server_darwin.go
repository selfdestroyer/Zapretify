package macdaemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Server is zapretifyd: it accepts commands on the socket and owns the engine.
type Server struct {
	OwnerUID int    // the user who installed the daemon; root is always allowed
	RootDir  string // RootDir in production
	Version  string

	mu       sync.Mutex // serialises commands and engine state
	proc     *engineProc
	rules    string // pf rules of the running strategy, reused on a network change
	lastExit string
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

// Startup clears what a previous, crashed daemon left behind.
func (s *Server) Startup() { cleanupStale(s.RootDir) }

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
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		reply(fmt.Errorf("неверный запрос: %w", err))
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Cmd {
	case CmdStatus:
	case CmdStart:
		var dataDir string
		if dataDir, err = userDataDir(uid, s.OwnerUID); err == nil {
			s.stopEngine()
			s.logf("запуск стратегии %q", req.Strategy)
			if err = s.startEngine(req.Strategy, dataDir); err != nil {
				s.logf("ошибка запуска: %v", err)
			}
		}
	case CmdStop:
		s.stopEngine()
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
	}
	r.LastExit = s.lastExit
	r.Log = s.log.tail(maxLogLines)
}

func (s *Server) logf(format string, args ...any) { log.Printf(format, args...) }

// userDataDir is the application data dir of the user the daemon works for; root acts for the owner.
func userDataDir(peerUID, ownerUID int) (string, error) {
	uid := peerUID
	if uid == 0 {
		uid = ownerUID
	}
	u, err := user.LookupId(fmt.Sprint(uid))
	if err != nil {
		return "", fmt.Errorf("пользователь %d: %w", uid, err)
	}
	return filepath.Join(u.HomeDir, UserDataSubdir), nil
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
