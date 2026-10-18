package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"zapretify/core/config"
	"zapretify/core/logger"
	"zapretify/core/platform"
	"zapretify/core/zapret"
)

//go:embed frontend/dist/*
var embeddedFrontend embed.FS

func main() {
	installSvc := flag.String("install-service", "", "Установить Zapret как системную службу Windows (требуются права администратора)")
	removeSvc := flag.Bool("remove-service", false, "Удалить системную службу Zapret")
	minimized := flag.Bool("minimized", false, "Запустить приложение свернутым в трей")
	uninstallFlag := flag.Bool("uninstall", false, "Удалить Zapretify и все компоненты программы")
	quietFlag := flag.Bool("quiet", false, "Тихий режим (без диалоговых окон)")
	flag.Parse()

	if *uninstallFlag {
		exePath, err := os.Executable()
		if err != nil {
			exePath = "."
		}
		appDir := filepath.Dir(exePath)
		handleUninstall(appDir, *quietFlag)
		return
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	appDir := filepath.Dir(exePath)
	binDir := filepath.Join(appDir, "bin")
	listsDir := filepath.Join(appDir, "lists")

	logger.Init(filepath.Join(appDir, "zapretify.log"))
	logger.Log("[Zapretify] Старт приложения v%s (%s/%s)", config.Version, runtime.GOOS, runtime.GOARCH)

	if *installSvc != "" {
		stratID := *installSvc
		if !validStrategy(stratID) {
			fmt.Printf("Ошибка: неизвестная стратегия '%s'\n", stratID)
			os.Exit(1)
		}
		cfg := config.Get()
		_ = zapret.SetIPSetMode(listsDir, cfg.Zapret.IPSetMode)
		err := zapret.InstallService(stratID, binDir, listsDir, cfg.Zapret.GameFilterMode, cfg.Zapret.GameFilterTCP, cfg.Zapret.GameFilterUDP)
		if err != nil {
			fmt.Printf("Ошибка установки службы: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Служба zapret успешно установлена со стратегией %s\n", stratID)
		os.Exit(0)
	}

	if *removeSvc {
		err := zapret.RemoveService(binDir)
		if err != nil {
			fmt.Printf("Ошибка удаления службы: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Служба zapret успешно удалена.")
		os.Exit(0)
	}

	// 1. Pre-elevation Single Instance Check
	// If an instance is already running (elevated or not), restore its window and exit immediately
	// WITHOUT showing the user an unnecessary UAC prompt!
	if isAlreadyRunning() {
		restoreExistingWindow()
		os.Exit(0)
	}

	if runtime.GOOS == "windows" && !platform.IsAdmin() && !*minimized {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	// 2. Single Instance Check (create/hold mutex for this running process)
	releaseMutex, isPrimary := ensureSingleInstance()
	if !isPrimary {
		logger.Log("[Zapretify] Обнаружена уже запущенная копия программы. Переключение на активное окно...")
		os.Exit(0)
	}
	defer releaseMutex()

	// 2. Initialize Core App
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := NewApp()
	app.startup(rootCtx)

	// Generate random 16-byte session token for local API protection
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Fatalf("Failed to generate session token: %v", err)
	}
	sessionToken := hex.EncodeToString(tokenBytes)

	// 3. Local Web GUI Server (Embedded, Ultra-lightweight ~15MB RAM)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Failed to bind local UI port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()

	// API Handlers
	registerAPIRoutes(mux, app)

	// Embedded Static Assets
	distFS, err := fs.Sub(embeddedFrontend, "frontend/dist")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(distFS)))
	} else {
		// Fallback to local files if not yet bundled
		mux.Handle("/", http.FileServer(http.Dir("frontend/dist")))
	}

	// Wrap mux with security middleware (DNS rebinding, CSRF, Token auth)
	securedHandler := securityMiddleware(sessionToken, port, mux)

	server := &http.Server{
		Handler:           securedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	guiURL := fmt.Sprintf("http://127.0.0.1:%d/?token=%s", port, sessionToken)
	log.Printf("[Zapretify] GUI запущен на http://127.0.0.1:%d\n", port)

	// 4. Graceful OS signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Log("[Zapretify] Получен сигнал завершения. Остановка всех процессов...")
		cancel()
		app.Shutdown()
		ExitGUIWindow()
		os.Exit(0)
	}()

	// 5. Run Native Desktop Window
	cfg := config.Get()
	startInTray := *minimized || cfg.App.StartMinimized
	runGUIWindow(guiURL, "Zapretify", 1060, 700, startInTray, app)

	// Clean shutdown when GUI window closes
	cancel()
	app.Shutdown()
	logger.Log("[Zapretify] Приложение успешно закрыто.")
}
