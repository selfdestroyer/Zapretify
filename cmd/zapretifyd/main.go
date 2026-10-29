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
	"os"
	"os/signal"
	"path/filepath"
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
	OwnerUID int `json:"owner_uid"`
}

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
