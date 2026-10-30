package macdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// bootState is what the daemon remembers between restarts: whether to start the engine at boot
// and with which strategy (the last one started). It lives in the root-owned folder.
type bootState struct {
	Boot     bool   `json:"boot"`
	Strategy string `json:"strategy"`
}

const (
	bootStateFile  = "state.json"
	bootRetry      = 10 * time.Second
	bootRetryLimit = 30 // five minutes: Wi-Fi may connect late after boot
)

func loadBootState(rootDir string) bootState {
	var st bootState
	if data, err := os.ReadFile(filepath.Join(rootDir, bootStateFile)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// saveState is called with s.mu held.
func (s *Server) saveState() {
	data, _ := json.Marshal(s.state)
	tmp := filepath.Join(s.RootDir, bootStateFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err == nil {
		_ = os.Rename(tmp, filepath.Join(s.RootDir, bootStateFile))
	}
}

// bootStart starts the remembered strategy once the network is up, retrying for a few minutes.
func (s *Server) bootStart() {
	for i := 0; i < bootRetryLimit; i++ {
		if i > 0 {
			time.Sleep(bootRetry)
		}
		if _, _, _, err := defaultRoute(); err != nil {
			continue // no network yet
		}
		s.mu.Lock()
		if s.proc != nil || !s.state.Boot {
			s.mu.Unlock()
			return // already started by the app, or switched off meanwhile
		}
		home, uid, err := userHome(s.OwnerUID, s.OwnerUID)
		if err == nil {
			s.logf("запуск при загрузке: стратегия %q", s.state.Strategy)
			err = s.startEngine(s.state.Strategy, home, uid)
		}
		s.mu.Unlock()
		if err == nil {
			return
		}
		s.logf("запуск при загрузке не удался: %v", err)
	}
}

// RemoveUserLaunchAgent deletes the login item Zapretify created for the owner
// (~/Library/LaunchAgents/io.github.selfdestroyer.zapretify.plist). The folders are opened with
// O_NOFOLLOW, so a symlink planted by the user cannot point the unlink elsewhere.
func RemoveUserLaunchAgent(home string) {
	dir, err := openBelow(home, "Library/LaunchAgents")
	if err != nil {
		return
	}
	defer closeFD(dir)
	_ = unlinkAt(dir, "io.github.selfdestroyer.zapretify.plist")
}
