package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	flag.Parse()

	if runtime.GOOS == "windows" && !platform.IsAdmin() && !*minimized {
		if err := platform.RunAsAdmin(os.Args[1:]...); err == nil {
			os.Exit(0)
		}
	}

	exePath, err := os.Executable()
	if err != nil {
		exePath = "."
	}
	appDir := filepath.Dir(exePath)
	binDir := filepath.Join(appDir, "bin")
	listsDir := filepath.Join(appDir, "lists")

	logger.Init(filepath.Join(appDir, "zapretify.log"))
	logger.Log("[Zapretify] Старт приложения v1.1.0 (%s/%s)", runtime.GOOS, runtime.GOARCH)

	if *installSvc != "" {
		stratID := *installSvc
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
		err := zapret.RemoveService()
		if err != nil {
			fmt.Printf("Ошибка удаления службы: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Служба zapret успешно удалена.")
		os.Exit(0)
	}

	// 2. Initialize Core App
	app := NewApp()
	app.startup(nil)

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

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = server.Serve(listener)
	}()

	guiURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	log.Printf("[Zapretify] GUI запущен на %s\n", guiURL)

	// 4. Graceful OS signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Log("[Zapretify] Получен сигнал завершения. Остановка всех процессов...")
		app.Shutdown()
		ExitGUIWindow()
		os.Exit(0)
	}()

	// 5. Run Native Desktop Window
	cfg := config.Get()
	startInTray := *minimized || cfg.App.StartMinimized
	runGUIWindow(guiURL, "Zapretify", 1060, 700, startInTray, app)

	// Clean shutdown when GUI window closes
	app.Shutdown()
	logger.Log("[Zapretify] Приложение успешно закрыто.")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
