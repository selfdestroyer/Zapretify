//go:build windows

package zapret

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"zapretify/core/logger"
	"zapretify/core/platform"
)

const (
	ServiceName = "zapret"
	DisplayName = "zapret"
)

func GetServiceStatus() (string, string, error) {
	stratName := ""
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `System\CurrentControlSet\Services\`+ServiceName, registry.QUERY_VALUE)
	if err == nil {
		val, _, _ := k.GetStringValue("zapret-strategy")
		stratName = val
		k.Close()
	}

	hSCM, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return getServiceStatusFallback(stratName)
	}
	defer windows.CloseServiceHandle(hSCM)

	serviceNamePtr, _ := windows.UTF16PtrFromString(ServiceName)
	hService, err := windows.OpenService(hSCM, serviceNamePtr, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return "NOT_INSTALLED", "", nil
	}
	defer windows.CloseServiceHandle(hService)

	var status windows.SERVICE_STATUS
	err = windows.QueryServiceStatus(hService, &status)
	if err != nil {
		return "STOPPED", stratName, err
	}

	switch status.CurrentState {
	case windows.SERVICE_RUNNING:
		return "RUNNING", stratName, nil
	case windows.SERVICE_START_PENDING:
		return "START_PENDING", stratName, nil
	case windows.SERVICE_STOP_PENDING:
		return "STOP_PENDING", stratName, nil
	default:
		return "STOPPED", stratName, nil
	}
}

func getServiceStatusFallback(stratName string) (string, string, error) {
	fallbackCmd := exec.Command("sc", "query", ServiceName)
	platform.HideWindow(fallbackCmd)
	out, _ := fallbackCmd.CombinedOutput()
	outStr := string(out)

	if strings.Contains(outStr, "1060") || strings.Contains(strings.ToLower(outStr), "not exist") {
		return "NOT_INSTALLED", "", nil
	}

	stateStr := "STOPPED"
	if strings.Contains(outStr, "RUNNING") {
		stateStr = "RUNNING"
	} else if strings.Contains(outStr, "START_PENDING") {
		stateStr = "START_PENDING"
	} else if strings.Contains(outStr, "STOP_PENDING") {
		stateStr = "STOP_PENDING"
	}

	return stateStr, stratName, nil
}

func InstallService(
	strategyID string,
	binDir string,
	listsDir string,
	gameFilterMode string,
	gameTCPRange string,
	gameUDPRange string,
) error {
	strat, ok := GetRegistry().Find(strategyID)
	if !ok {
		return fmt.Errorf("стратегия не найдена: %s", strategyID)
	}

	// 1. Ensure all user list files exist
	if err := EnsureUserLists(listsDir); err != nil {
		return fmt.Errorf("ошибка создания файлов списков: %v", err)
	}

	// 2. Terminate previous stuck instances of OUR winws.exe only (never other tools' processes)
	platform.KillImageInDir("winws.exe", binDir)

	// 3. Enable TCP timestamps
	_ = platform.RunHidden("netsh", "interface", "tcp", "set", "global", "timestamps=enabled")

	// 4. Construct ImagePath command line
	rawArgs := buildProcessedRawArgs(strat.RawArgs, binDir, listsDir, gameFilterMode, gameTCPRange, gameUDPRange)
	winwsExe := filepath.Join(binDir, "winws.exe")
	binaryPath := fmt.Sprintf(`"%s" %s`, winwsExe, rawArgs)

	hSCM, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err != nil {
		return fmt.Errorf("ошибка доступа к диспетчеру служб Windows (требуются права администратора): %v", err)
	}
	defer windows.CloseServiceHandle(hSCM)

	serviceNamePtr, _ := windows.UTF16PtrFromString(ServiceName)
	displayNamePtr, _ := windows.UTF16PtrFromString(DisplayName)
	binaryPathPtr, _ := windows.UTF16PtrFromString(binaryPath)

	// Check if service already exists to avoid DELETE_PENDING errors
	hService, openErr := windows.OpenService(hSCM, serviceNamePtr, windows.SERVICE_ALL_ACCESS)
	if openErr == nil {
		// Stop if running
		var status windows.SERVICE_STATUS
		_ = windows.ControlService(hService, windows.SERVICE_CONTROL_STOP, &status)
		time.Sleep(150 * time.Millisecond)

		// Update service configuration in place
		err = windows.ChangeServiceConfig(
			hService,
			windows.SERVICE_NO_CHANGE,
			windows.SERVICE_AUTO_START,
			windows.SERVICE_ERROR_NORMAL,
			binaryPathPtr,
			nil, nil, nil, nil, nil,
			displayNamePtr,
		)
		if err != nil {
			windows.CloseServiceHandle(hService)
			return fmt.Errorf("ошибка обновления конфигурации службы: %v", err)
		}
	} else {
		// Create new service
		hService, err = windows.CreateService(
			hSCM,
			serviceNamePtr,
			displayNamePtr,
			windows.SERVICE_ALL_ACCESS,
			windows.SERVICE_WIN32_OWN_PROCESS,
			windows.SERVICE_AUTO_START,
			windows.SERVICE_ERROR_NORMAL,
			binaryPathPtr,
			nil, nil, nil, nil, nil,
		)
		if err != nil {
			return fmt.Errorf("ошибка создания службы: %v", err)
		}
	}
	defer windows.CloseServiceHandle(hService)

	// Set Description and metadata in registry
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `System\CurrentControlSet\Services\`+ServiceName, registry.SET_VALUE); err == nil {
		_ = k.SetStringValue("Description", "Zapret DPI bypass software")
		_ = k.SetStringValue("zapret-strategy", strat.Name)
		_ = k.SetStringValue("zapret-discord-youtube", strat.ID)
		k.Close()
	}

	// 5. Start service
	err = windows.StartService(hService, 0, nil)
	if err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("служба создана, но запуск не удался: %v", err)
	}

	// 6. Wait for service to become RUNNING
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		var status windows.SERVICE_STATUS
		if err := windows.QueryServiceStatus(hService, &status); err == nil {
			if status.CurrentState == windows.SERVICE_RUNNING {
				return nil
			}
			if status.CurrentState == windows.SERVICE_STOPPED {
				return fmt.Errorf("служба зарегистрирована, но процесс winws.exe аварийно остановился (ExitCode: %d, Win32: %d). Проверьте файлы в bin/ и lists/",
					status.ServiceSpecificExitCode, status.Win32ExitCode)
			}
		}
	}

	return nil
}

func RemoveService(binDir string) error {
	platform.KillImageInDir("winws.exe", binDir)

	hSCM, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		serviceNamePtr, _ := windows.UTF16PtrFromString(ServiceName)
		hService, err := windows.OpenService(hSCM, serviceNamePtr, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS|windows.DELETE)
		if err == nil {
			var status windows.SERVICE_STATUS
			_ = windows.ControlService(hService, windows.SERVICE_CONTROL_STOP, &status)
			_ = windows.DeleteService(hService)
			windows.CloseServiceHandle(hService)
		}
		windows.CloseServiceHandle(hSCM)
	}

	_ = platform.RunHidden("net", "stop", "WinDivert")
	_ = platform.RunHidden("sc", "delete", "WinDivert")
	_ = platform.RunHidden("net", "stop", "WinDivert14")
	_ = platform.RunHidden("sc", "delete", "WinDivert14")
	time.Sleep(300 * time.Millisecond)
	return nil
}

func StartService(binDir string) error {
	platform.KillImageInDir("winws.exe", binDir)

	hSCM, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("ошибка диспетчера служб: %v", err)
	}
	defer windows.CloseServiceHandle(hSCM)

	serviceNamePtr, _ := windows.UTF16PtrFromString(ServiceName)
	hService, err := windows.OpenService(hSCM, serviceNamePtr, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return fmt.Errorf("служба zapret не установлена в системе")
	}
	defer windows.CloseServiceHandle(hService)

	_ = platform.RunHidden("netsh", "interface", "tcp", "set", "global", "timestamps=enabled")

	err = windows.StartService(hService, 0, nil)
	if err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("ошибка вызова StartService: %v", err)
	}

	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		var status windows.SERVICE_STATUS
		if err := windows.QueryServiceStatus(hService, &status); err == nil {
			if status.CurrentState == windows.SERVICE_RUNNING {
				return nil
			}
			if status.CurrentState == windows.SERVICE_STOPPED {
				return fmt.Errorf("служба запустилась, но аварийно остановилась (ExitCode: %d, Win32: %d)",
					status.ServiceSpecificExitCode, status.Win32ExitCode)
			}
		}
	}
	return nil
}

// StopService stops the "zapret" Windows service and the winws.exe it launched.
// winws.exe is not a real service executable and does not answer service control requests,
// so after asking SCM to stop the service we terminate the winws.exe processes located in binDir.
// Processes of the same name started from other folders (other tools) are never touched.
func StopService(binDir string) error {
	hSCM, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err == nil {
		serviceNamePtr, _ := windows.UTF16PtrFromString(ServiceName)
		hService, err := windows.OpenService(hSCM, serviceNamePtr, windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
		if err == nil {
			var status windows.SERVICE_STATUS
			if err := windows.QueryServiceStatus(hService, &status); err == nil && status.CurrentState != windows.SERVICE_STOPPED {
				_ = windows.ControlService(hService, windows.SERVICE_CONTROL_STOP, &status)
			}
			windows.CloseServiceHandle(hService)
		}
		windows.CloseServiceHandle(hSCM)
	}

	if n := platform.KillImageInDir("winws.exe", binDir); n > 0 {
		logger.Log("[Zapret] Завершено процессов winws.exe из %s: %d", binDir, n)
	}
	time.Sleep(200 * time.Millisecond)
	return nil
}
