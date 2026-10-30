//go:build darwin

// zapretifyd is the root daemon of Zapretify for macOS. launchd starts it from
// /Library/LaunchDaemons/io.github.selfdestroyer.zapretifyd.plist; the application sends it
// commands over /var/run/zapretifyd.sock (see core/macdaemon).
//
// With arguments it is a client for checks from Terminal:
//
//	zapretifyd status | start <strategy-id> | stop
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"zapretify/core/config"
	"zapretify/core/macdaemon"
)

func client(args []string) {
	req := macdaemon.Request{Cmd: args[0]}
	if req.Cmd == macdaemon.CmdStart {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "использование: zapretifyd start <strategy-id>")
			os.Exit(2)
		}
		req.Strategy = args[1]
	}
	resp, err := macdaemon.Call(req, 60*time.Second)
	out, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(out))
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

// daemonConfig is written by the installer next to the daemon.
type daemonConfig struct {
	OwnerUID int    `json:"owner_uid"`
	AppPath  string `json:"app_path"` // Zapretify.app that installed the daemon; empty for Terminal installs
}

// appGoneAfter: the app may disappear for a moment while it is being replaced by a newer version.
const appGoneAfter = 10 * time.Minute

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("zapretifyd: ")
	if len(os.Args) > 1 {
		client(os.Args[1:])
		return
	}
	if os.Geteuid() != 0 {
		log.Fatal("должен работать от root")
	}
	cfg, err := readConfig(filepath.Join(macdaemon.RootDir, "daemon.json"))
	if err != nil {
		log.Fatal(err)
	}

	s := &macdaemon.Server{OwnerUID: cfg.OwnerUID, RootDir: macdaemon.RootDir, Version: config.Version}
	s.Startup()
	l, err := macdaemon.Listen(macdaemon.SocketPath)
	if err != nil {
		log.Fatal(err)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		log.Print("остановка")
		s.Shutdown()
		l.Close()
	}()

	log.Printf("версия %s, владелец uid %d", config.Version, cfg.OwnerUID)
	go watchApp(cfg, s, l)
	if err := s.Serve(l); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(macdaemon.SocketPath)
}

// readConfig accepts only a root-owned file that nobody else can change: the owner uid decides
// who may control the engine.
func readConfig(path string) (daemonConfig, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return daemonConfig{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.Mode().IsRegular() || !ok || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
		return daemonConfig{}, fmt.Errorf("%s должен принадлежать root и быть недоступен для записи другим", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return daemonConfig{}, err
	}
	var cfg daemonConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return daemonConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if cfg.OwnerUID <= 0 {
		return daemonConfig{}, fmt.Errorf("%s: не указан owner_uid", path)
	}
	return cfg, nil
}

// watchApp removes the daemon when Zapretify.app is gone (moved to the Trash): deleting the app
// is all a user needs to uninstall Zapretify. The user's settings and lists stay in their home.
func watchApp(cfg daemonConfig, s *macdaemon.Server, l net.Listener) {
	if cfg.AppPath == "" {
		return
	}
	var gone time.Time
	for range time.Tick(time.Minute) {
		// Only "does not exist" counts: privacy protection (TCC) may deny root access to
		// ~/Downloads or ~/Desktop, which must not uninstall anything.
		if _, err := os.Stat(cfg.AppPath); !os.IsNotExist(err) {
			gone = time.Time{}
			continue
		}
		if gone.IsZero() {
			gone = time.Now()
			log.Printf("%s не найден; если не появится за %s, служба удалит себя", cfg.AppPath, appGoneAfter)
			continue
		}
		if time.Since(gone) < appGoneAfter {
			continue
		}
		log.Print("Zapretify.app удалён — служба удаляет себя")
		s.Shutdown()
		l.Close()
		if u, err := user.LookupId(strconv.Itoa(cfg.OwnerUID)); err == nil {
			macdaemon.RemoveUserLaunchAgent(u.HomeDir)
		}
		_ = os.Remove("/Library/LaunchDaemons/" + macdaemon.Label + ".plist")
		_ = os.Remove(macdaemon.SocketPath)
		_ = os.Remove("/var/run/zapretify.conf")
		_ = os.RemoveAll(macdaemon.RootDir)
		_ = os.RemoveAll("/Library/Logs/Zapretify")
		// bootout stops this process; launchd does not start it again, the plist is gone.
		_ = exec.Command("/bin/launchctl", "bootout", "system/"+macdaemon.Label).Run()
		os.Exit(0)
	}
}
