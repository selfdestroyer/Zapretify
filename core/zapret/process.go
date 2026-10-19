package zapret

import (
	"bytes"
	"errors"
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

// startupGrace is how long Start waits to make sure winws.exe did not die immediately
// (bad arguments, missing driver, conflict with another WinDivert user, no admin rights, ...).
var startupGrace = 700 * time.Millisecond

// stopWaitTimeout is how long Stop waits for the process to disappear.
var stopWaitTimeout = 3 * time.Second

const maxTailLines = 20

type ProcessRunner struct {
	cmd            *exec.Cmd
	done           chan struct{}
	activeStrategy string
	pidFile        string
	mu             sync.Mutex

	registry *StrategyRegistry

	logCallbacks []func(string)
	cbMu         sync.Mutex

	// last lines printed by winws.exe, used for diagnostics when it exits unexpectedly
	tailMu sync.Mutex
	tail   []string

	// newCommand builds the child process; replaced in tests.
	newCommand func(winwsExe string, args []string, binDir string) *exec.Cmd
}

var (
	runnerInstance *ProcessRunner
	runnerOnce     sync.Once
)

// NewProcessRunner creates a runner that resolves strategies from the given registry
// (the global registry is used when reg is nil).
func NewProcessRunner(reg *StrategyRegistry) *ProcessRunner {
	return &ProcessRunner{registry: reg}
}

func GetRunner() *ProcessRunner {
	runnerOnce.Do(func() {
		runnerInstance = NewProcessRunner(nil)
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

func (p *ProcessRunner) addTail(line string) {
	p.tailMu.Lock()
	defer p.tailMu.Unlock()
	p.tail = append(p.tail, line)
	if len(p.tail) > maxTailLines {
		p.tail = p.tail[len(p.tail)-maxTailLines:]
	}
}

func (p *ProcessRunner) resetTail() {
	p.tailMu.Lock()
	p.tail = nil
	p.tailMu.Unlock()
}

func (p *ProcessRunner) lastOutput() string {
	p.tailMu.Lock()
	defer p.tailMu.Unlock()
	return strings.Join(p.tail, " | ")
}

// lineWriter splits process output into lines. It is used as cmd.Stdout/Stderr so that
// os/exec itself guarantees that all output is delivered before Wait returns.
type lineWriter struct {
	prefix string
	emit   func(string)
	mu     sync.Mutex
	buf    []byte
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if line != "" {
			w.emit(w.prefix + line)
		}
	}
	if len(w.buf) > 64*1024 { // runaway line without newline
		w.emit(w.prefix + string(w.buf))
		w.buf = nil
	}
	return len(b), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if rest := strings.TrimRight(string(w.buf), "\r\n"); rest != "" {
		w.emit(w.prefix + rest)
	}
	w.buf = nil
}

func defaultCommand(winwsExe string, args []string, binDir string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.Command(winwsExe, args...)
		cmd.Dir = binDir
		return cmd
	}
	// Development mock on macOS/Linux: a long-running harmless process.
	return exec.Command("sleep", "3600")
}

func (p *ProcessRunner) Start(
	strategyID string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) error {
	reg := p.registry
	if reg == nil {
		reg = GetRegistry()
	}
	strat, ok := reg.Find(strategyID)
	if !ok {
		return fmt.Errorf("unknown strategy: %s", strategyID)
	}

	args := BuildArgs(strat.RawArgs, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange)
	winwsExe := filepath.Join(binDir, "winws.exe")

	p.mu.Lock()
	if p.cmd != nil && p.cmd.Process != nil {
		p.mu.Unlock()
		return fmt.Errorf("process already running")
	}

	factory := p.newCommand
	if factory == nil {
		factory = defaultCommand
	}
	cmd := factory(winwsExe, args, binDir)
	platform.HideWindow(cmd)

	p.resetTail()
	emit := func(line string) {
		p.addTail(line)
		p.emitLog(line)
	}
	outW := &lineWriter{prefix: "[winws] ", emit: emit}
	errW := &lineWriter{prefix: "[winws err] ", emit: emit}
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.WaitDelay = 2 * time.Second // never hang if a grandchild keeps the pipes open

	p.emitLog(fmt.Sprintf("Запуск Zapret [%s]...", strat.Name))
	if err := cmd.Start(); err != nil {
		p.mu.Unlock()
		p.emitLog(fmt.Sprintf("Ошибка запуска winws.exe: %v", err))
		return err
	}

	done := make(chan struct{})
	p.cmd = cmd
	p.done = done
	p.activeStrategy = strategyID
	pid := cmd.Process.Pid
	p.savePID(binDir, pid)
	p.mu.Unlock()

	// Watch the process: it may crash or be killed from outside (taskkill, antivirus, ...).
	go func() {
		waitErr := cmd.Wait()
		outW.Flush()
		errW.Flush()

		p.mu.Lock()
		unexpected := p.cmd == cmd
		if unexpected {
			p.cmd = nil
			p.done = nil
			p.activeStrategy = ""
			p.removePID()
		}
		p.mu.Unlock()

		if unexpected {
			code := 0
			var ee *exec.ExitError
			if errors.As(waitErr, &ee) {
				code = ee.ExitCode()
			}
			p.emitLog(fmt.Sprintf("[winws] Процесс winws (PID %d) завершил работу (код %d).", pid, code))
		}
		close(done)
	}()

	select {
	case <-done:
		msg := "winws.exe завершился сразу после запуска"
		tail := p.lastOutput()
		if strings.Contains(tail, "already running with the same filter") {
			// Свои процессы и службу StartZapret уже остановил: значит, это winws из другой папки
			// (другая копия Zapret, GoodbyeDPI-подобная программа). Без согласия пользователя его не трогаем.
			msg = "уже запущен другой winws.exe с тем же фильтром (другая копия Zapret или похожая программа). " +
				"Закройте её или нажмите «Диагностика и сброс» → «Завершить Zapret»"
		} else if tail != "" {
			msg += ": " + tail
		}
		p.emitLog("Ошибка: " + msg)
		return errors.New(msg)
	case <-time.After(startupGrace):
	}

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
// The process is terminated only if the PID from winws.pid still belongs to <binDir>\winws.exe;
// otherwise (process gone, PID reused by something else, image path unknown) nothing is killed.
func CleanupOrphans(binDir string) {
	pidPath := getPIDFilePath(binDir)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	_ = os.Remove(pidPath)

	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil || pid <= 0 {
		return
	}

	img, err := platform.ProcessImagePath(pid)
	if err != nil {
		logger.Log("[Zapret] PID %d из winws.pid не найден или недоступен (%v). Пропуск.", pid, err)
		return
	}
	if !platform.SamePath(img, filepath.Join(binDir, "winws.exe")) {
		logger.Log("[Zapret] PID %d из winws.pid принадлежит другому процессу (%s). Пропуск.", pid, img)
		return
	}

	logger.Log("[Zapret] Обнаружен незавершенный winws.exe (PID %d) от предыдущего сеанса. Завершение процесса...", pid)
	if err := platform.TerminateProcess(pid); err != nil {
		logger.Log("[Zapret] Не удалось завершить PID %d: %v", pid, err)
	}
}

// Stop terminates the process started by this runner (and only it) and waits until it is gone.
func (p *ProcessRunner) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	done := p.done
	p.cmd = nil
	p.done = nil
	p.activeStrategy = ""
	p.removePID()
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	p.emitLog("Остановка Zapret...")
	pid := cmd.Process.Pid
	if runtime.GOOS == "windows" {
		_ = platform.RunHidden("taskkill", "/PID", fmt.Sprintf("%d", pid), "/F", "/T")
	}
	_ = cmd.Process.Kill()

	if done != nil {
		select {
		case <-done:
		case <-time.After(stopWaitTimeout):
			p.emitLog(fmt.Sprintf("Предупреждение: процесс winws (PID %d) не завершился за %s.", pid, stopWaitTimeout))
		}
	}
	p.emitLog("Zapret остановлен.")
	return nil
}
