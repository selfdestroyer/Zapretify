package zapret

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"zapretify/core/logger"
)

type ProcessRunner struct {
	cmd            *exec.Cmd
	activeStrategy string
	mu             sync.Mutex

	logCallbacks   []func(string)
	cbMu           sync.Mutex

	stopChan       chan struct{}
}

var (
	runnerInstance *ProcessRunner
	runnerOnce     sync.Once
)

func GetRunner() *ProcessRunner {
	runnerOnce.Do(func() {
		runnerInstance = &ProcessRunner{}
	})
	return runnerInstance
}

func (p *ProcessRunner) AddLogListener(cb func(string)) {
	p.cbMu.Lock()
	defer p.cbMu.Unlock()
	p.logCallbacks = append(p.logCallbacks, cb)
}

func (p *ProcessRunner) emitLog(msg string) {
	logger.Log("%s", msg)

	p.cbMu.Lock()
	cbs := append([]func(string){}, p.logCallbacks...)
	p.cbMu.Unlock()

	for _, cb := range cbs {
		cb(msg)
	}
}

func (p *ProcessRunner) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	// Check if process has exited
	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		p.cmd = nil
		p.activeStrategy = ""
		return false
	}
	return true
}

func (p *ProcessRunner) GetActiveStrategy() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.activeStrategy
}

func (p *ProcessRunner) Start(
	strategyID string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd != nil && p.cmd.Process != nil && (p.cmd.ProcessState == nil || !p.cmd.ProcessState.Exited()) {
		return fmt.Errorf("process already running")
	}

	strat, ok := GetRegistry().Find(strategyID)
	if !ok {
		return fmt.Errorf("unknown strategy: %s", strategyID)
	}

	args := BuildArgs(strat.RawArgs, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange)
	winwsExe := filepath.Join(binDir, "winws.exe")

	p.emitLog(fmt.Sprintf("Запуск Zapret [%s]...", strat.Name))

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command(winwsExe, args...)
		cmd.Dir = binDir
	} else {
		// Mock on macOS/Linux for development
		cmd = exec.Command("cat")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	if err := cmd.Start(); err != nil {
		p.emitLog(fmt.Sprintf("Ошибка запуска winws.exe: %v", err))
		return err
	}

	p.cmd = cmd
	p.activeStrategy = strategyID
	p.stopChan = make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			p.emitLog(fmt.Sprintf("[winws] %s", scanner.Text()))
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			p.emitLog(fmt.Sprintf("[winws err] %s", scanner.Text()))
		}
	}()

	time.Sleep(300 * time.Millisecond)
	p.emitLog(fmt.Sprintf("Zapret успешно активен (PID: %d)", cmd.Process.Pid))
	return nil
}

func (p *ProcessRunner) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.emitLog("Остановка Zapret...")
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/IM", "winws.exe", "/F", "/T").Run()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		done := make(chan error, 1)
		go func(c *exec.Cmd) {
			done <- c.Wait()
		}(p.cmd)
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
		}
	}
	p.cmd = nil
	p.activeStrategy = ""
	p.emitLog("Zapret остановлен.")
	return nil
}
