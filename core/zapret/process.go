package zapret

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"zapretify/core/logger"
	"zapretify/core/platform"
)

type ProcessRunner struct {
	cmd            *exec.Cmd
	activeStrategy string
	pidFile        string
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
	return p.cmd != nil && p.cmd.Process != nil
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

	if p.cmd != nil && p.cmd.Process != nil {
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

	platform.HideWindow(cmd)

	if err := cmd.Start(); err != nil {
		p.emitLog(fmt.Sprintf("Ошибка запуска winws.exe: %v", err))
		return err
	}

	p.cmd = cmd
	p.activeStrategy = strategyID
	p.stopChan = make(chan struct{})
	pid := cmd.Process.Pid
	p.savePID(binDir, pid)

	// Goroutine watching process exit (e.g. crash or external taskkill)
	go func(c *exec.Cmd, expectedPID int) {
		_ = c.Wait()
		p.mu.Lock()
		if p.cmd == c {
			p.cmd = nil
			p.activeStrategy = ""
			p.removePID()
			p.emitLog(fmt.Sprintf("[winws] Процесс winws (PID %d) завершил работу.", expectedPID))
		}
		p.mu.Unlock()
	}(cmd, pid)

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
	p.emitLog(fmt.Sprintf("Zapret успешно активен (PID: %d)", pid))
	return nil
}

func getPIDFilePath(binDir string) string {
	return filepath.Join(binDir, "winws.pid")
}

func (p *ProcessRunner) savePID(binDir string, pid int) {
	p.pidFile = getPIDFilePath(binDir)
	_ = os.WriteFile(p.pidFile, []byte(fmt.Sprintf("%d", pid)), 0644)
}

func (p *ProcessRunner) removePID() {
	if p.pidFile != "" {
		_ = os.Remove(p.pidFile)
		p.pidFile = ""
	}
}

// CleanupOrphans checks if a previous instance crashed and left its own winws process running.
// It verifies that the PID actually belongs to winws.exe before terminating it.
func CleanupOrphans(binDir string) {
	pidPath := getPIDFilePath(binDir)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	_ = os.Remove(pidPath)

	pidStr := strings.TrimSpace(string(data))
	var pid int
	if _, err := fmt.Sscanf(pidStr, "%d", &pid); err != nil || pid <= 0 {
		return
	}

	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").CombinedOutput()
		if err != nil || !strings.Contains(strings.ToLower(string(out)), "winws.exe") {
			logger.Log("[Zapret] PID %d из winws.pid не принадлежит winws.exe (процесс завершен или PID переиспользован). Пропуск taskkill.", pid)
			return
		}
	}

	logger.Log("[Zapret] Обнаружен незавершенный PID %d (winws.exe) от предыдущего сеанса. Завершение процесса...", pid)
	_ = platform.RunHidden("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F", "/T")
}

func (p *ProcessRunner) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.emitLog("Остановка Zapret...")
	if p.cmd != nil && p.cmd.Process != nil {
		pid := p.cmd.Process.Pid
		_ = platform.RunHidden("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F", "/T")
		_ = p.cmd.Process.Kill()
	}
	p.removePID()
	p.cmd = nil
	p.activeStrategy = ""
	p.emitLog("Zapret остановлен.")
	return nil
}
