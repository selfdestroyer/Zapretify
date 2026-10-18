package zapret

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newTestRunner(t *testing.T, factory func(string, []string, string) *exec.Cmd) (*ProcessRunner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell as a fake winws.exe")
	}
	old := startupGrace
	startupGrace = 250 * time.Millisecond
	t.Cleanup(func() { startupGrace = old })

	reg := &StrategyRegistry{Strategies: []Strategy{{ID: "general", Name: "General", RawArgs: "--wf-tcp=80"}}}
	r := NewProcessRunner(reg)
	r.newCommand = factory
	return r, t.TempDir()
}

func sleepCmd(string, []string, string) *exec.Cmd { return exec.Command("sleep", "30") }

func TestRunnerStartStop(t *testing.T) {
	r, bin := newTestRunner(t, sleepCmd)

	if err := r.Start("general", bin, bin, "disabled", "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.IsRunning() || r.GetActiveStrategy() != "general" {
		t.Fatalf("runner must be running with strategy general")
	}
	if _, err := os.Stat(filepath.Join(bin, "winws.pid")); err != nil {
		t.Fatalf("pid file must exist: %v", err)
	}
	if err := r.Start("general", bin, bin, "disabled", "", ""); err == nil {
		t.Fatalf("second Start must fail while running")
	}

	if err := r.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if r.IsRunning() || r.GetActiveStrategy() != "" {
		t.Fatalf("runner must be stopped")
	}
	if _, err := os.Stat(filepath.Join(bin, "winws.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file must be removed after Stop")
	}
}

func TestRunnerUnknownStrategy(t *testing.T) {
	r, bin := newTestRunner(t, sleepCmd)
	if err := r.Start("nope", bin, bin, "disabled", "", ""); err == nil {
		t.Fatalf("unknown strategy must be rejected")
	}
	if r.IsRunning() {
		t.Fatalf("nothing must be started")
	}
}

func TestRunnerReportsImmediateExit(t *testing.T) {
	r, bin := newTestRunner(t, func(string, []string, string) *exec.Cmd {
		return exec.Command("sh", "-c", "echo 'cannot open WinDivert' >&2; exit 3")
	})

	err := r.Start("general", bin, bin, "disabled", "", "")
	if err == nil {
		t.Fatalf("Start must report that the process died right away")
	}
	if !strings.Contains(err.Error(), "WinDivert") {
		t.Fatalf("error must carry the last process output, got: %v", err)
	}
	if r.IsRunning() {
		t.Fatalf("runner must not be running")
	}
	if _, statErr := os.Stat(filepath.Join(bin, "winws.pid")); !os.IsNotExist(statErr) {
		t.Fatalf("pid file must be removed")
	}
}

func TestRunnerDetectsExternalKill(t *testing.T) {
	r, bin := newTestRunner(t, sleepCmd)
	if err := r.Start("general", bin, bin, "disabled", "", ""); err != nil {
		t.Fatal(err)
	}

	r.mu.Lock()
	proc := r.cmd.Process
	r.mu.Unlock()
	_ = proc.Kill() // simulates "taskkill /F" from outside

	deadline := time.Now().Add(3 * time.Second)
	for r.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if r.IsRunning() {
		t.Fatalf("runner must notice that the process was killed externally")
	}
	if r.GetActiveStrategy() != "" {
		t.Fatalf("active strategy must be cleared")
	}
	if _, err := os.Stat(filepath.Join(bin, "winws.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file must be removed")
	}
}

func TestCleanupOrphansNeverKillsUnverifiedProcess(t *testing.T) {
	bin := t.TempDir()
	// PID of the current test process: it is NOT <bin>\winws.exe and must survive.
	if err := os.WriteFile(filepath.Join(bin, "winws.pid"), []byte(strings.TrimSpace(strings.Repeat("1", 1))+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	CleanupOrphans(bin)
	if _, err := os.Stat(filepath.Join(bin, "winws.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file must always be removed")
	}
}

func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{prefix: "> ", emit: func(s string) { got = append(got, s) }}
	_, _ = w.Write([]byte("one\r\ntw"))
	_, _ = w.Write([]byte("o\n\nthree"))
	w.Flush()
	want := []string{"> one", "> two", "> three"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}
