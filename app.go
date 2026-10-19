package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"zapretify/core/config"
	"zapretify/core/logger"
	"zapretify/core/platform"
	"zapretify/core/tgproxy"
	"zapretify/core/updater"
	"zapretify/core/zapret"
)

func runHidden(name string, args ...string) error {
	return platform.RunHidden(name, args...)
}

type SingleBenchmarkStatus struct {
	Running    bool                    `json:"running"`
	Done       bool                    `json:"done"`
	StrategyID string                  `json:"strategy_id"`
	Result     *zapret.BenchmarkResult `json:"result,omitempty"`
	Error      string                  `json:"error,omitempty"`
}

type App struct {
	ctx          context.Context
	appDir       string
	binDir       string
	listsDir     string
	shutdownOnce sync.Once

	cfg      *config.Config
	runner   *zapret.ProcessRunner
	registry *zapret.StrategyRegistry
	tgServer *tgproxy.Server

	benchMu     sync.RWMutex
	benchStatus SingleBenchmarkStatus

	syncMu     sync.RWMutex
	syncStatus AssetSyncStatus
}

// AssetSyncStatus describes the background synchronisation of host lists and strategies.
type AssetSyncStatus struct {
	Running bool                        `json:"running"`
	Done    bool                        `json:"done"`
	Summary *updater.AssetUpdateSummary `json:"summary,omitempty"`
	Error   string                      `json:"error,omitempty"`
}

// zapretState remembers what was running before a benchmark so it can be restored afterwards.
type zapretState struct {
	serviceRunning bool
	procRunning    bool
	strategy       string
}

func NewApp() *App {
	return NewAppWith(
		config.Get(),
		zapret.GetRunner(),
		zapret.GetRegistry(),
		tgproxy.GetServer(),
	)
}

func NewAppWith(cfg *config.Config, runner *zapret.ProcessRunner, registry *zapret.StrategyRegistry, tgServer *tgproxy.Server) *App {
	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	appDir := filepath.Dir(exePath)
	binDir := filepath.Join(appDir, "bin")
	listsDir := filepath.Join(appDir, "lists")

	_ = zapret.EnsureUserLists(listsDir)

	return &App{
		appDir:   appDir,
		binDir:   binDir,
		listsDir: listsDir,
		cfg:      cfg,
		runner:   runner,
		registry: registry,
		tgServer: tgServer,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Clean up any orphan winws process left over from a previous unexpected crash
	zapret.CleanupOrphans(a.binDir)

	// Autostart services ONLY if explicitly configured by user
	cfg := a.cfg.Clone()
	if cfg.TGProxy.Autostart {
		if err := a.StartTGProxy(); err != nil {
			logger.Log("[App] Автозапуск Telegram прокси не удался: %v", err)
		}
	}
	if cfg.Zapret.AutostartService && cfg.Zapret.ServiceMode {
		if err := a.StartZapret(cfg.Zapret.Strategy); err != nil {
			logger.Log("[App] Автозапуск Zapret не удался: %v", err)
		}
	}

	// The real autostart registration (scheduled task / Run key) may have been created by the
	// installer or removed manually: make the stored setting reflect reality.
	go a.reconcileAutostart()

	go func() {
		time.Sleep(500 * time.Millisecond)
		TriggerTrayUpdate()
	}()
}

func (a *App) reconcileAutostart() {
	// One-way sync only: if autostart is registered in the system but the setting says "off"
	// (installer option), switch the setting on. A failed query as a non-elevated user must never
	// switch a user's choice off.
	if a.cfg.Clone().App.AutostartApp || !config.IsAppAutostart() {
		return
	}
	logger.Log("[App] В системе найден автозапуск Zapretify (создан установщиком); настройка синхронизирована.")
	if err := a.cfg.Update(func(c *config.Config) { c.App.AutostartApp = true }); err != nil {
		logger.Log("[App] Не удалось сохранить состояние автозапуска: %v", err)
	}
}

// Config
func (a *App) GetConfig() *config.Config {
	return a.cfg.Clone()
}

func (a *App) SaveConfig(cfg *config.Config) error {
	// Register/unregister autostart only when the user actually changed it. Doing it on every save
	// would silently undo an autostart created outside of the application (e.g. by the installer).
	if cfg.App.AutostartApp != a.cfg.Clone().App.AutostartApp {
		if err := config.SetAppAutostart(cfg.App.AutostartApp); err != nil {
			return fmt.Errorf("автозапуск: %w", err)
		}
	}
	return a.cfg.Update(func(c *config.Config) {
		c.App = cfg.App
		c.Zapret = cfg.Zapret
		c.TGProxy = cfg.TGProxy
	})
}

// Zapret
func (a *App) GetStrategies() []zapret.Strategy {
	if a.registry == nil {
		return nil
	}
	return a.registry.List()
}

func (a *App) IsValidStrategy(id string) bool {
	if id == "" {
		return true
	}
	if a.registry == nil {
		return true
	}
	_, ok := a.registry.Find(id)
	return ok
}

func (a *App) GetZapretStatus() map[string]interface{} {
	cfg := a.cfg.Clone()
	svcState, stratName, _ := zapret.GetServiceStatus()
	procRunning := false
	if a.runner != nil {
		procRunning = a.runner.IsRunning()
	}

	isRunning := svcState == "RUNNING" || procRunning

	return map[string]interface{}{
		"is_running":        isRunning,
		"service_state":     svcState,
		"service_strategy":  stratName,
		"proc_running":      procRunning,
		"current_strategy":  cfg.Zapret.Strategy,
		"service_mode":      cfg.Zapret.ServiceMode,
		"autostart_service": cfg.Zapret.AutostartService,
		"autostart_app":     cfg.App.AutostartApp,
	}
}

func (a *App) IsZapretRunning() bool {
	svcState, _, _ := zapret.GetServiceStatus()
	procRunning := false
	if a.runner != nil {
		procRunning = a.runner.IsRunning()
	}
	return svcState == "RUNNING" || procRunning
}

func (a *App) StartZapret(strategyID string) error {
	if a.isBenchmarkRunning() {
		return fmt.Errorf("идёт тестирование стратегий — дождитесь его завершения")
	}

	cfg := a.cfg.Clone()
	if strategyID == "" {
		strategyID = cfg.Zapret.Strategy
		// A stale/unknown strategy ID in config.json must not prevent starting.
		if a.registry != nil {
			if _, ok := a.registry.Find(strategyID); !ok {
				if def, ok := a.registry.Default(); ok {
					logger.Log("[Zapret] Стратегия '%s' из конфигурации не найдена. Используется '%s'.", strategyID, def.ID)
					strategyID = def.ID
				}
			}
		}
	}

	logger.Log("[Zapret] Запуск стратегии '%s' (service_mode=%v)...", strategyID, cfg.Zapret.ServiceMode)

	_ = zapret.EnsureUserLists(a.listsDir)
	_ = zapret.SetIPSetMode(a.listsDir, cfg.Zapret.IPSetMode)

	// Stop any previously running process runner for this app before starting
	if a.runner != nil {
		_ = a.runner.Stop()
	}

	if cfg.Zapret.ServiceMode {
		status, installedStrategy, _ := zapret.GetServiceStatus()
		logger.Log("[Zapret] Текущее состояние службы SCM: %s", status)

		// The service has its strategy baked into its command line. If another strategy was
		// requested, the service must be reinstalled, otherwise the choice would be silently ignored.
		if (status == "RUNNING" || status == "STOPPED") && a.registry != nil && platform.IsAdmin() {
			if strat, ok := a.registry.Find(strategyID); ok && installedStrategy != "" && installedStrategy != strat.Name {
				logger.Log("[Zapret] Служба установлена со стратегией '%s', запрошена '%s'. Переустановка службы...", installedStrategy, strat.Name)
				if err := zapret.InstallService(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP); err == nil {
					logger.Log("[Zapret] Служба переустановлена с новой стратегией.")
					return nil
				} else {
					logger.Log("[Zapret] Не удалось переустановить службу: %v", err)
				}
			}
		}

		if status == "RUNNING" {
			logger.Log("[Zapret] Служба уже активна.")
			return nil
		}
		if status == "STOPPED" {
			err := zapret.StartService(a.binDir)
			if err == nil {
				logger.Log("[Zapret] Служба Windows SCM успешно запущена.")
				return nil
			}
			logger.Log("[Zapret] Ошибка старта существующей службы: %v. Попытка переустановки...", err)
			if platform.IsAdmin() {
				err = zapret.InstallService(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
				if err == nil {
					logger.Log("[Zapret] Служба переустановлена и запущена.")
					return nil
				}
				logger.Log("[Zapret] Ошибка установки службы: %v", err)
			}
		}
		if status == "NOT_INSTALLED" && platform.IsAdmin() {
			err := zapret.InstallService(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
			if err == nil {
				logger.Log("[Zapret] Служба установлена и запущена.")
				return nil
			}
			logger.Log("[Zapret] Ошибка установки новой службы: %v", err)
		}
		logger.Log("[Zapret] Служба недоступна или завершилась с ошибкой. Откат на переносимый процесс runner...")
	}

	// Portable process runner
	if a.runner == nil {
		return fmt.Errorf("process runner is not initialized")
	}
	// Тот же фильтр WinDivert может держать служба zapret (запущена, хотя режим службы выключен, или
	// не смогла стартовать выше) либо winws.exe из нашей папки bin, оставшийся от прошлого сеанса.
	// Тогда новый winws сразу завершается с «already running with the same filter». StopService
	// останавливает службу и завершает только winws.exe из a.binDir; чужие копии не трогаются.
	if err := zapret.StopService(a.binDir); err != nil {
		logger.Log("[Zapret] Не удалось освободить фильтр перед запуском: %v", err)
	}
	err := a.runner.Start(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
	if err != nil {
		logger.Log("[Zapret] Ошибка запуска процесса runner: %v", err)
		return err
	}
	logger.Log("[Zapret] Фоновый процесс runner успешно запущен.")
	return nil
}

func (a *App) StopZapret() error {
	logger.Log("[Zapret] Запрос остановки...")
	if a.runner != nil {
		_ = a.runner.Stop()
	}
	_ = zapret.StopService(a.binDir)
	time.Sleep(200 * time.Millisecond)
	logger.Log("[Zapret] Остановка завершена.")
	return nil
}

// isNoSuchProcess reports whether a failed "taskkill" only means that there was nothing to kill.
func isNoSuchProcess(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == 128
}

// ForceKillZapret is the explicit "emergency reset" button: unlike the regular Stop it terminates
// EVERY winws.exe in the system (the user asked for a full reset), so the UI asks for confirmation.
func (a *App) ForceKillZapret() error {
	logger.Log("[Zapret] Аварийный сброс: завершение всех процессов winws.exe...")
	var errs []error
	if a.runner != nil {
		if err := a.runner.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := zapret.StopService(a.binDir); err != nil {
		errs = append(errs, err)
	}
	if runtime.GOOS == "windows" {
		if err := runHidden("taskkill", "/IM", "winws.exe", "/F", "/T"); err != nil && !isNoSuchProcess(err) {
			errs = append(errs, fmt.Errorf("taskkill: %w", err))
		}
	}
	time.Sleep(200 * time.Millisecond)
	logger.Log("[Zapret] Аварийный сброс завершен.")
	return errors.Join(errs...)
}

// Shutdown гарантированно и полностью останавливает Zapret, WinDivert, Telegram Proxy и все зависимости
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		logger.Log("[App] ===================================================")
		logger.Log("[App] Завершение работы: остановка Zapret, tg-ws-proxy и всех зависимостей...")

		// 0. Остановка одиночного бенчмарка (если активен)
		a.benchMu.Lock()
		if a.benchStatus.Running {
			a.benchStatus.Running = false
			a.benchStatus.Done = true
			a.benchStatus.Error = "aborted by shutdown"
		}
		a.benchMu.Unlock()

		// 1. Остановка авто-тестирования/бенчмарка (если активно)
		zapret.StopAutoBenchmark()

		// 2. Остановка runner-процесса Zapret (winws.exe)
		logger.Log("[App] Остановка фонового процесса Zapret (winws.exe)...")
		if a.runner != nil {
			_ = a.runner.Stop()
		}

		// 3. Остановка системной службы Windows zapret (если активна)
		logger.Log("[App] Остановка системной службы Windows zapret...")
		_ = zapret.StopService(a.binDir)

		// 4. Остановка Telegram Proxy
		logger.Log("[App] Остановка Telegram прокси...")
		_ = a.StopTGProxy()

		logger.Log("[App] Все службы, процессы Zapret и Telegram прокси успешно завершены.")
		logger.Log("[App] ===================================================")
	})
}

func (a *App) ForceExitAll() error {
	a.Shutdown()

	// Закрытие окон и гарантированное завершение процесса
	go func() {
		time.Sleep(100 * time.Millisecond)
		ExitGUIWindow()
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()

	return nil
}

func (a *App) ResetDrivers() error {
	logger.Log("[Zapret] Полный сброс служб и драйвера WinDivert...")
	killErr := a.ForceKillZapret()
	_ = zapret.RemoveService(a.binDir)
	// "not loaded"/"not installed" are normal results here, so errors of these commands are ignored.
	_ = runHidden("net", "stop", "WinDivert")
	_ = runHidden("sc", "delete", "WinDivert")
	_ = runHidden("net", "stop", "WinDivert14")
	_ = runHidden("sc", "delete", "WinDivert14")
	time.Sleep(400 * time.Millisecond)
	logger.Log("[Zapret] Сброс WinDivert завершен.")
	return killErr
}

func (a *App) InstallZapretService(strategyID string) error {
	cfg := a.cfg.Clone()
	if strategyID == "" {
		strategyID = cfg.Zapret.Strategy
	}
	_ = zapret.SetIPSetMode(a.listsDir, cfg.Zapret.IPSetMode)
	if !platform.IsAdmin() {
		return platform.RunAsAdmin("--install-service", strategyID)
	}
	return zapret.InstallService(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
}

func (a *App) RemoveZapretService() error {
	if !platform.IsAdmin() {
		return platform.RunAsAdmin("--remove-service")
	}
	return zapret.RemoveService(a.binDir)
}

// Telegram Proxy
func (a *App) StartTGProxy() error {
	if a.tgServer == nil {
		return fmt.Errorf("tgproxy server is not initialized")
	}
	cfg := a.cfg.Clone()
	return a.tgServer.Start(
		cfg.TGProxy.Host,
		cfg.TGProxy.Port,
		cfg.TGProxy.Secret,
		cfg.TGProxy.CFProxy,
		cfg.TGProxy.H2,
		cfg.TGProxy.CFWorkerDomain,
		cfg.TGProxy.CFUserDomain,
		cfg.TGProxy.ForceTestDC,
	)
}

func (a *App) StopTGProxy() error {
	if a.tgServer == nil {
		return nil
	}
	return a.tgServer.Stop()
}

func (a *App) GetTGProxyStatus() bool {
	if a.tgServer == nil {
		return false
	}
	return a.tgServer.IsRunning()
}

func (a *App) GetTGProxyLink() string {
	if a.tgServer == nil {
		return ""
	}
	return a.tgServer.GetProxyLink()
}

func (a *App) PingTelegramDCs() map[string]float64 {
	dcs := map[string]string{
		"DC 1 (Miami)":     "149.154.175.50",
		"DC 2 (Amsterdam)": "149.154.167.220",
		"DC 3 (Miami)":     "149.154.175.100",
		"DC 4 (Amsterdam)": "149.154.167.91",
		"DC 5 (Singapore)": "91.108.56.165",
	}

	results := make(map[string]float64)
	for name, ip := range dcs {
		ok, lat := tgproxy.PingDC(ip, 443, 2*time.Second)
		if ok {
			results[name] = lat
		} else {
			results[name] = -1
		}
	}
	return results
}

// Tester & Diagnostics

// isBenchmarkRunning reports whether any strategy test (single or all strategies) is in progress.
func (a *App) isBenchmarkRunning() bool {
	a.benchMu.RLock()
	single := a.benchStatus.Running
	a.benchMu.RUnlock()
	return single || zapret.GetAutoBenchmarkStatus().Running
}

// snapshotZapretState records what is currently running so that it can be restored after a benchmark.
func (a *App) snapshotZapretState() zapretState {
	st := zapretState{}
	svcState, _, _ := zapret.GetServiceStatus()
	st.serviceRunning = svcState == "RUNNING"
	if a.runner != nil {
		st.procRunning = a.runner.IsRunning()
		st.strategy = a.runner.GetActiveStrategy()
	}
	return st
}

// restoreZapretState brings back the protection that was active before a benchmark stopped it.
func (a *App) restoreZapretState(st zapretState) {
	if !st.serviceRunning && !st.procRunning {
		return
	}
	logger.Log("[Benchmark] Восстановление Zapret, который работал до тестирования...")
	if st.serviceRunning {
		if err := zapret.StartService(a.binDir); err != nil {
			logger.Log("[Benchmark] Не удалось запустить службу Zapret обратно: %v", err)
		}
		return
	}
	if err := a.StartZapret(st.strategy); err != nil {
		logger.Log("[Benchmark] Не удалось восстановить Zapret: %v", err)
	}
}

func (a *App) RunStrategyBenchmark(strategyID string, method string, targetGroup string) (zapret.BenchmarkResult, error) {
	cfg := a.cfg.Clone()
	if strategyID == "" {
		strategyID = cfg.Zapret.Strategy
	}
	if method == "" {
		method = "all"
	}
	if targetGroup == "" {
		targetGroup = "all"
	}

	logger.Log("[Benchmark] Запрос тестирования стратегии '%s' (метод: %s, цели: %s)...", strategyID, method, targetGroup)

	// Remember what was running; it is restored when the test is over.
	prev := a.snapshotZapretState()
	defer a.restoreZapretState(prev)

	// 1. Обязательно завершаем запущенный до этого запрет перед началом проверки
	_ = a.StopZapret()
	time.Sleep(300 * time.Millisecond)

	stratName := strategyID
	if a.registry != nil {
		if strat, ok := a.registry.Find(strategyID); ok && strat != nil {
			stratName = strat.Name
		}
	}

	// 2. Временно запускаем проверяемую стратегию. Если winws не стартовал, замер был бы сделан
	// без обхода блокировок и дал бы ложный результат, поэтому тест прерывается с ошибкой.
	logger.Log("[Benchmark] Запуск стратегии '%s' для замера доступности...", stratName)
	if a.runner == nil {
		return zapret.BenchmarkResult{}, fmt.Errorf("process runner is not initialized")
	}
	if err := a.runner.Start(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP); err != nil {
		logger.Log("[Benchmark] Стратегия '%s' не запустилась: %v", stratName, err)
		return zapret.BenchmarkResult{}, fmt.Errorf("не удалось запустить стратегию %q: %w", stratName, err)
	}
	time.Sleep(300 * time.Millisecond)

	// 3. Выполняем проверку доступности целей
	res := zapret.BenchmarkCurrentStrategy(strategyID, stratName, method, targetGroup)

	// Записываем результат проверки в отдельный текстовый файл в папке программы (перезаписывая его)
	reportPath := filepath.Join(a.appDir, "test_results.txt")
	if err := zapret.SaveBenchmarkReport(reportPath, res); err != nil {
		logger.Log("[Benchmark] Ошибка записи отчета в %s: %v", reportPath, err)
	} else {
		logger.Log("[Benchmark] Отчет тестирования стратегии сохранен в: %s", reportPath)
	}

	// 4. Завершаем проверяемую стратегию (прежнее состояние Zapret вернётся через defer)
	logger.Log("[Benchmark] Тест стратегии '%s' завершен (%d/%d доступно).", stratName, res.SuccessCount, res.TotalCount)
	_ = a.runner.Stop()
	time.Sleep(200 * time.Millisecond)

	return res, nil
}

func (a *App) StartSingleBenchmark(strategyID string, method string, targetGroup string) error {
	if zapret.GetAutoBenchmarkStatus().Running {
		return fmt.Errorf("выполняется тестирование всех стратегий")
	}

	a.benchMu.Lock()
	if a.benchStatus.Running {
		a.benchMu.Unlock()
		return fmt.Errorf("тестирование стратегии уже выполняется")
	}
	a.benchStatus = SingleBenchmarkStatus{
		Running:    true,
		Done:       false,
		StrategyID: strategyID,
	}
	a.benchMu.Unlock()

	go func() {
		var (
			res zapret.BenchmarkResult
			err error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("внутренняя ошибка теста: %v", r)
				}
			}()
			res, err = a.RunStrategyBenchmark(strategyID, method, targetGroup)
		}()

		a.benchMu.Lock()
		a.benchStatus.Running = false
		a.benchStatus.Done = true
		if err != nil {
			a.benchStatus.Error = err.Error()
		} else {
			a.benchStatus.Result = &res
		}
		a.benchMu.Unlock()
	}()

	return nil
}

func (a *App) GetSingleBenchmarkStatus() SingleBenchmarkStatus {
	a.benchMu.RLock()
	defer a.benchMu.RUnlock()
	return a.benchStatus
}

func (a *App) StartAutoBenchmark(method string, targetGroup string) error {
	a.benchMu.RLock()
	singleRunning := a.benchStatus.Running
	a.benchMu.RUnlock()
	if singleRunning {
		return fmt.Errorf("выполняется тестирование одной стратегии")
	}

	cfg := a.cfg.Clone()
	if method == "" {
		method = "all"
	}
	if targetGroup == "" {
		targetGroup = "all"
	}
	logger.Log("[AutoBenchmark] Запрос проверки ВСЕХ стратегий (метод: %s, цели: %s). Остановка активного Zapret перед началом...", method, targetGroup)

	prev := a.snapshotZapretState()
	// Обязательно завершаем запущенный до этого запрет
	_ = a.StopZapret()
	time.Sleep(300 * time.Millisecond)
	err := zapret.StartAutoBenchmark(a.runner, a.registry, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP, method, targetGroup, a.appDir, func() {
		a.restoreZapretState(prev)
	})
	if err != nil {
		// The benchmark did not start: bring back what we have just stopped.
		a.restoreZapretState(prev)
	}
	return err
}

func (a *App) OpenTestLog() error {
	reportPath := filepath.Join(a.appDir, "test_results.txt")
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		return fmt.Errorf("файл отчета test_results.txt еще не создан. Сначала проведите тест.")
	}
	return platform.OpenFile(reportPath)
}

func (a *App) GetTestLog() (string, error) {
	reportPath := filepath.Join(a.appDir, "test_results.txt")
	b, err := os.ReadFile(reportPath)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (a *App) GetAutoBenchmarkStatus() zapret.AutoBenchmarkStatus {
	return zapret.GetAutoBenchmarkStatus()
}

// StopAutoBenchmark прерывает подбор; прежнее состояние Zapret вернёт onFinish из StartAutoBenchmark.
func (a *App) StopAutoBenchmark() {
	logger.Log("[AutoBenchmark] Запрошена остановка подбора стратегий")
	zapret.StopAutoBenchmark()
}

func (a *App) GetLastBenchmark() (*zapret.LastBenchmark, error) {
	return zapret.LoadLastBenchmark(a.appDir)
}

// BenchmarkActivity — короткая сводка для /api/status: главный экран показывает «Подбор · N из M»,
// не опрашивая отдельно статус подбора.
type BenchmarkActivity struct {
	Running bool   `json:"running"`
	Mode    string `json:"mode,omitempty"` // "all" — подбор, "one" — проверка одной стратегии
	Tested  int    `json:"tested"`
	Total   int    `json:"total"`
}

func (a *App) GetBenchmarkActivity() BenchmarkActivity {
	if st := zapret.GetAutoBenchmarkStatus(); st.Running {
		return BenchmarkActivity{Running: true, Mode: "all", Tested: st.TestedCount, Total: st.TotalStrategies}
	}
	if a.isBenchmarkRunning() {
		return BenchmarkActivity{Running: true, Mode: "one"}
	}
	return BenchmarkActivity{}
}

func (a *App) GetTargetsByGroup(group string) []zapret.TargetEndpoint {
	return zapret.GetTargetsByGroup(group)
}

func (a *App) GetIPSetMode() string {
	return zapret.GetIPSetMode(a.listsDir)
}

func (a *App) SetIPSetMode(mode string) error {
	if err := zapret.SetIPSetMode(a.listsDir, mode); err != nil {
		return err
	}
	return a.cfg.Update(func(c *config.Config) {
		c.Zapret.IPSetMode = mode
	})
}

func (a *App) GetDiagnostics() platform.DiagnosticReport {
	return platform.RunDiagnostics(a.binDir)
}

// Updaters
func (a *App) CheckAppUpdate() (*updater.UpdateCheckResult, error) {
	return updater.CheckAppUpdate(config.DefaultGithubRepo)
}

// StartAssetSync downloads host lists and strategies from upstream in the background.
// It can take longer than an HTTP request is allowed to, so the UI polls GetAssetSyncStatus.
func (a *App) StartAssetSync() error {
	a.syncMu.Lock()
	if a.syncStatus.Running {
		a.syncMu.Unlock()
		return fmt.Errorf("синхронизация уже выполняется")
	}
	a.syncStatus = AssetSyncStatus{Running: true}
	a.syncMu.Unlock()

	go func() {
		var (
			summary *updater.AssetUpdateSummary
			err     error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("внутренняя ошибка синхронизации: %v", r)
				}
			}()
			summary, err = updater.UpdateUpstreamAssets(a.listsDir, a.appDir)
		}()
		if err == nil {
			if a.registry != nil {
				if loadErr := a.registry.Load(); loadErr != nil {
					logger.Log("[Updater] Не удалось перечитать strategies.json: %v", loadErr)
				}
			}
			// Downloaded ipset-all.txt replaces the file that stores the IPSet mode: re-apply the mode.
			if modeErr := zapret.SetIPSetMode(a.listsDir, a.cfg.Clone().Zapret.IPSetMode); modeErr != nil {
				logger.Log("[Updater] Не удалось применить режим IPSet после обновления: %v", modeErr)
			}
		}

		a.syncMu.Lock()
		a.syncStatus.Running = false
		a.syncStatus.Done = true
		if err != nil {
			a.syncStatus.Error = err.Error()
		} else {
			a.syncStatus.Summary = summary
		}
		a.syncMu.Unlock()
	}()
	return nil
}

func (a *App) GetAssetSyncStatus() AssetSyncStatus {
	a.syncMu.RLock()
	defer a.syncMu.RUnlock()
	return a.syncStatus
}

// Lists Editor
func (a *App) ReadList(filename string) (string, error) {
	mgr := zapret.NewListManager(a.listsDir)
	return mgr.ReadRaw(filename)
}

func (a *App) SaveList(filename string, content string) error {
	mgr := zapret.NewListManager(a.listsDir)
	return mgr.WriteRaw(filename, zapret.EnsureNotEmptyUserList(filename, content))
}

// Autostart
func (a *App) SetAutostartApp(enabled bool) error {
	if err := config.SetAppAutostart(enabled); err != nil {
		return err
	}
	return a.cfg.Update(func(c *config.Config) {
		c.App.AutostartApp = enabled
	})
}

func (a *App) RestartAsAdmin() error {
	return platform.RunAsAdmin()
}

func (a *App) FixTCPTimestamps() error {
	return platform.FixTCPTimestamps()
}

// Logs and Diagnostics
func (a *App) GetLogs() []string {
	return logger.GetLines()
}

func (a *App) ClearLogs() error {
	logger.Clear()
	return nil
}

func (a *App) GenerateDebugReport() string {
	var b strings.Builder
	b.WriteString("=== ZAPRETIFY ДИАГНОСТИЧЕСКИЙ ОТЧЕТ ===\n")
	b.WriteString(fmt.Sprintf("Время: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	b.WriteString(fmt.Sprintf("ОС: %s (%s)\n", runtime.GOOS, runtime.GOARCH))
	b.WriteString(fmt.Sprintf("Права администратора: %v\n", platform.IsAdmin()))

	cfg := a.cfg.Clone()
	b.WriteString(fmt.Sprintf("Конфигурация: strategy=%s, service_mode=%v, autostart=%v\n",
		cfg.Zapret.Strategy, cfg.Zapret.ServiceMode, cfg.App.AutostartApp))

	svcState, svcStrat, _ := zapret.GetServiceStatus()
	b.WriteString(fmt.Sprintf("Служба zapret: состояние=%s, стратегия=%s\n", svcState, svcStrat))
	procRunning := false
	if a.runner != nil {
		procRunning = a.runner.IsRunning()
	}
	b.WriteString(fmt.Sprintf("Процесс runner активен: %v\n", procRunning))

	if runtime.GOOS == "windows" {
		// Query WinDivert driver status
		out, err := platform.OutputHidden("sc", "query", "windivert")
		if err == nil {
			b.WriteString(fmt.Sprintf("\n--- Драйвер WinDivert ---\n%s\n", strings.TrimSpace(string(out))))
		}

		// Query running winws processes
		outProc, _ := platform.OutputHidden("tasklist", "/FI", "IMAGENAME eq winws.exe")
		b.WriteString(fmt.Sprintf("\n--- Процессы winws.exe ---\n%s\n", strings.TrimSpace(string(outProc))))

		// TCP Timestamps & Diagnostics
		diag := platform.RunDiagnostics(a.binDir)
		b.WriteString(fmt.Sprintf("\n--- Диагностика системы ---\nTCP Timestamps OK: %v\nGoodbyeDPI конфликт: %v\nWinDivert файлы: %v\n",
			diag.TCPTimestampsOK, diag.GoodbyeDPIRun, diag.WinDivertExists))
	}

	b.WriteString("\n--- ПОСЛЕДНИЕ СОБЫТИЯ И ЛОГИ (zapretify.log) ---\n")
	lines := logger.GetLines()
	startIdx := 0
	if len(lines) > 80 {
		startIdx = len(lines) - 80
	}
	for _, l := range lines[startIdx:] {
		b.WriteString(l + "\n")
	}
	b.WriteString("=== КОНЕЦ ОТЧЕТА ===\n")
	return b.String()
}
