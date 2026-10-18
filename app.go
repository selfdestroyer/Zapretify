package main

import (
	"context"
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

type App struct {
	ctx          context.Context
	appDir       string
	binDir       string
	listsDir     string
	shutdownOnce sync.Once
}

func NewApp() *App {
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
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Clean up any orphan winws process left over from a previous unexpected crash
	zapret.CleanupOrphans(a.binDir)

	// Autostart services ONLY if explicitly configured by user
	cfg := config.Get()
	if cfg.TGProxy.Autostart {
		_ = a.StartTGProxy()
	}
	if cfg.Zapret.AutostartService && cfg.Zapret.ServiceMode {
		_ = a.StartZapret(cfg.Zapret.Strategy)
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		TriggerTrayUpdate()
	}()
}

// Config
func (a *App) GetConfig() *config.Config {
	return config.Get().Clone()
}

func (a *App) SaveConfig(cfg *config.Config) error {
	if err := config.SetAppAutostart(cfg.App.AutostartApp); err != nil {
		return fmt.Errorf("автозапуск: %w", err)
	}
	return config.Get().Update(func(c *config.Config) {
		c.App = cfg.App
		c.Zapret = cfg.Zapret
		c.TGProxy = cfg.TGProxy
	})
}

// Zapret
func (a *App) GetStrategies() []zapret.Strategy {
	return zapret.GetRegistry().List()
}

func (a *App) GetZapretStatus() map[string]interface{} {
	cfg := config.Get()
	svcState, stratName, _ := zapret.GetServiceStatus()
	procRunning := zapret.GetRunner().IsRunning()

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
	return svcState == "RUNNING" || zapret.GetRunner().IsRunning()
}

func (a *App) StartZapret(strategyID string) error {
	cfg := config.Get()
	if strategyID == "" {
		strategyID = cfg.Zapret.Strategy
	}

	logger.Log("[Zapret] Запуск стратегии '%s' (service_mode=%v)...", strategyID, cfg.Zapret.ServiceMode)

	_ = zapret.EnsureUserLists(a.listsDir)
	_ = zapret.SetIPSetMode(a.listsDir, cfg.Zapret.IPSetMode)

	// Stop any previously running process runner for this app before starting
	_ = zapret.GetRunner().Stop()

	if cfg.Zapret.ServiceMode {
		status, _, _ := zapret.GetServiceStatus()
		logger.Log("[Zapret] Текущее состояние службы SCM: %s", status)
		if status == "RUNNING" {
			logger.Log("[Zapret] Служба уже активна.")
			return nil
		}
		if status == "STOPPED" {
			err := zapret.StartService()
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
	err := zapret.GetRunner().Start(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
	if err != nil {
		logger.Log("[Zapret] Ошибка запуска процесса runner: %v", err)
		return err
	}
	logger.Log("[Zapret] Фоновый процесс runner успешно запущен.")
	return nil
}

func (a *App) StopZapret() error {
	logger.Log("[Zapret] Запрос остановки...")
	_ = zapret.GetRunner().Stop()
	_ = zapret.StopService()
	time.Sleep(200 * time.Millisecond)
	logger.Log("[Zapret] Остановка завершена.")
	return nil
}

func (a *App) ForceKillZapret() error {
	logger.Log("[Zapret] Аварийный сброс: Kill winws.exe...")
	errRunner := zapret.GetRunner().Stop()
	errSvc := zapret.StopService()
	errKill := runHidden("taskkill", "/IM", "winws.exe", "/F", "/T")
	time.Sleep(200 * time.Millisecond)
	logger.Log("[Zapret] Все процессы winws.exe принудительно завершены.")
	if errRunner != nil {
		return errRunner
	}
	if errSvc != nil {
		return errSvc
	}
	return errKill
}

// Shutdown гарантированно и полностью останавливает Zapret, WinDivert, Telegram Proxy и все зависимости
func (a *App) Shutdown() {
	a.shutdownOnce.Do(func() {
		logger.Log("[App] ===================================================")
		logger.Log("[App] Завершение работы: остановка Zapret, tg-ws-proxy и всех зависимостей...")

		// 1. Остановка авто-тестирования/бенчмарка (если активно)
		zapret.StopAutoBenchmark()

		// 2. Остановка runner-процесса Zapret (winws.exe)
		logger.Log("[App] Остановка фонового процесса Zapret (winws.exe)...")
		_ = zapret.GetRunner().Stop()

		// 3. Остановка системной службы Windows zapret (если активна)
		logger.Log("[App] Остановка системной службы Windows zapret...")
		_ = zapret.StopService()

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
	_ = a.ForceKillZapret()
	_ = zapret.RemoveService()
	_ = runHidden("net", "stop", "WinDivert")
	_ = runHidden("sc", "delete", "WinDivert")
	_ = runHidden("net", "stop", "WinDivert14")
	_ = runHidden("sc", "delete", "WinDivert14")
	time.Sleep(400 * time.Millisecond)
	logger.Log("[Zapret] Сброс WinDivert завершен.")
	return nil
}

func (a *App) InstallZapretService(strategyID string) error {
	cfg := config.Get()
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
	return zapret.RemoveService()
}

// Telegram Proxy
func (a *App) StartTGProxy() error {
	cfg := config.Get()
	return tgproxy.GetServer().Start(
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
	return tgproxy.GetServer().Stop()
}

func (a *App) GetTGProxyStatus() bool {
	return tgproxy.GetServer().IsRunning()
}

func (a *App) GetTGProxyLink() string {
	return tgproxy.GetServer().GetProxyLink()
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
func (a *App) RunStrategyBenchmark(strategyID string, method string, targetGroup string) zapret.BenchmarkResult {
	cfg := config.Get()
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

	// 1. Обязательно завершаем запущенный до этого запрет перед началом проверки
	_ = a.StopZapret()
	time.Sleep(300 * time.Millisecond)

	strat, ok := zapret.GetRegistry().Find(strategyID)
	stratName := strategyID
	if ok && strat != nil {
		stratName = strat.Name
	}

	// 2. Временно запускаем проверяемую стратегию
	logger.Log("[Benchmark] Запуск стратегии '%s' для замера доступности...", stratName)
	_ = zapret.GetRunner().Start(strategyID, a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
	time.Sleep(800 * time.Millisecond)

	// 3. Выполняем проверку доступности целей
	res := zapret.BenchmarkCurrentStrategy(strategyID, stratName, method, targetGroup)

	// Записываем результат проверки в отдельный текстовый файл в папке программы (перезаписывая его)
	reportPath := filepath.Join(a.appDir, "test_results.txt")
	if err := zapret.SaveBenchmarkReport(reportPath, res); err != nil {
		logger.Log("[Benchmark] Ошибка записи отчета в %s: %v", reportPath, err)
	} else {
		logger.Log("[Benchmark] Отчет тестирования стратегии сохранен в: %s", reportPath)
	}

	// 4. Обязательно завершаем стратегию после проверки - запрет остается остановленным
	logger.Log("[Benchmark] Тест стратегии '%s' завершен (%d/%d доступно). Запрет остановлен, выбор за пользователем.",
		stratName, res.SuccessCount, res.TotalCount)
	_ = zapret.GetRunner().Stop()
	time.Sleep(200 * time.Millisecond)

	return res
}

func (a *App) StartAutoBenchmark(method string, targetGroup string) error {
	cfg := config.Get()
	if method == "" {
		method = "all"
	}
	if targetGroup == "" {
		targetGroup = "all"
	}
	logger.Log("[AutoBenchmark] Запрос проверки ВСЕХ стратегий (метод: %s, цели: %s). Остановка активного Zapret перед началом...", method, targetGroup)
	// Обязательно завершаем запущенный до этого запрет
	_ = a.StopZapret()
	time.Sleep(300 * time.Millisecond)
	return zapret.StartAutoBenchmark(a.binDir, a.listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP, method, targetGroup, a.appDir)
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
	return config.Get().Update(func(c *config.Config) {
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

func (a *App) PerformAppUpdate(downloadURL string) error {
	return updater.PerformInPlaceUpdate(downloadURL)
}

func (a *App) UpdateAssets() (*updater.AssetUpdateSummary, error) {
	summary, err := updater.UpdateUpstreamAssets(a.listsDir, a.appDir)
	if err == nil {
		_ = zapret.GetRegistry().Load()
	}
	return summary, err
}

// Lists Editor
func (a *App) ReadList(filename string) (string, error) {
	mgr := zapret.NewListManager(a.listsDir)
	return mgr.ReadRaw(filename)
}

func (a *App) SaveList(filename string, content string) error {
	mgr := zapret.NewListManager(a.listsDir)
	return mgr.WriteRaw(filename, content)
}

// Autostart
func (a *App) SetAutostartApp(enabled bool) error {
	if err := config.SetAppAutostart(enabled); err != nil {
		return err
	}
	return config.Get().Update(func(c *config.Config) {
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

	cfg := config.Get()
	b.WriteString(fmt.Sprintf("Конфигурация: strategy=%s, service_mode=%v, autostart=%v\n",
		cfg.Zapret.Strategy, cfg.Zapret.ServiceMode, cfg.App.AutostartApp))

	svcState, svcStrat, _ := zapret.GetServiceStatus()
	b.WriteString(fmt.Sprintf("Служба zapret: состояние=%s, стратегия=%s\n", svcState, svcStrat))
	b.WriteString(fmt.Sprintf("Процесс runner активен: %v\n", zapret.GetRunner().IsRunning()))

	if runtime.GOOS == "windows" {
		// Query WinDivert driver status
		out, err := exec.Command("sc", "query", "windivert").CombinedOutput()
		if err == nil {
			b.WriteString(fmt.Sprintf("\n--- Драйвер WinDivert ---\n%s\n", strings.TrimSpace(string(out))))
		}

		// Query running winws processes
		outProc, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq winws.exe").CombinedOutput()
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
