package macdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T, owner int) string {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes; t.TempDir() on macOS can be longer.
	dir, err := os.MkdirTemp("/tmp", "zd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	l, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := &Server{OwnerUID: owner, RootDir: dir, Version: "test"}
	go s.Serve(l)
	return sock
}

func TestServerStatusForOwner(t *testing.T) {
	sock := startTestServer(t, os.Getuid())
	resp, err := call(sock, Request{Cmd: CmdStatus}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Running || resp.Version != "test" || resp.Protocol != ProtocolVersion {
		t.Fatalf("unexpected status %+v", resp)
	}
	if _, err := call(sock, Request{Cmd: "rm -rf"}, 5*time.Second); err == nil || !strings.Contains(err.Error(), "неизвестная команда") {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestServerRefusesOtherUsers(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root is always allowed")
	}
	sock := startTestServer(t, os.Getuid()+1)
	_, err := call(sock, Request{Cmd: CmdStop}, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "только от пользователя") {
		t.Fatalf("expected refusal, got %v", err)
	}
}
