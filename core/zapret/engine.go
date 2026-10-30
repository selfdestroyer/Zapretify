package zapret

// Engine runs the DPI bypass engine with one strategy at a time.
// On Windows it is winws.exe started as a child process (ProcessRunner); other platforms
// provide their own implementation (on macOS the engine runs under a root daemon).
// The application, the single strategy test and the auto benchmark use only this interface.
type Engine interface {
	// Start launches the engine with the given strategy and returns once it is known to be running.
	Start(strategyID, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange string) error
	// Stop terminates the engine started by Start and waits until it is gone; no-op if not running.
	Stop() error
	IsRunning() bool
	// GetActiveStrategy returns the ID of the running strategy, or "" when stopped.
	GetActiveStrategy() string
}

var _ Engine = (*ProcessRunner)(nil)
