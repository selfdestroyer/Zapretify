package macdaemon

import (
	"fmt"
	"time"

	"zapretify/core/logger"
	"zapretify/core/zapret"
)

// startTimeout covers importing the bases, translating the strategy and the engine's start-up check.
const (
	startTimeout  = 60 * time.Second
	statusTimeout = 5 * time.Second
)

// DaemonEngine is zapret.Engine on macOS: the engine runs under zapretifyd, the application only
// sends commands. binDir/listsDir are not passed: the daemon imports them itself from the user's
// data dir (UserDataSubdir), which is where the application keeps them on macOS.
type DaemonEngine struct{}

var _ zapret.Engine = DaemonEngine{}

func (DaemonEngine) Start(strategyID, _, _, gameFilterMode, _, _ string) error {
	if gameFilterMode != "" && gameFilterMode != "disabled" {
		logger.Log("[Zapret] Game Filter на macOS пока не поддерживается, режим %q игнорируется", gameFilterMode)
	}
	logger.Log("[Zapret] Запуск стратегии '%s' через службу Zapretify...", strategyID)
	resp, err := Call(Request{Cmd: CmdStart, Strategy: strategyID}, startTimeout)
	if err != nil {
		return err
	}
	if !resp.Running {
		return fmt.Errorf("служба Zapretify не запустила движок")
	}
	logger.Log("[Zapret] Zapret активен (PID %d, интерфейс %s)", resp.PID, resp.Iface)
	if resp.Note != "" {
		logger.Log("[Zapret] %s", resp.Note)
	}
	return nil
}

func (DaemonEngine) Stop() error {
	_, err := Call(Request{Cmd: CmdStop}, startTimeout)
	if err == nil {
		logger.Log("[Zapret] Zapret остановлен.")
	}
	return err
}

func (DaemonEngine) IsRunning() bool {
	resp, err := Call(Request{Cmd: CmdStatus}, statusTimeout)
	return err == nil && resp.Running
}

func (DaemonEngine) GetActiveStrategy() string {
	resp, err := Call(Request{Cmd: CmdStatus}, statusTimeout)
	if err != nil || !resp.Running {
		return ""
	}
	return resp.Strategy
}
