package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type DiagnosticReport struct {
	IsAdmin         bool   `json:"is_admin"`
	GoodbyeDPIRun   bool   `json:"goodbyedpi_running"`
	WinDivertExists bool   `json:"windivert_exists"`
	TCPTimestampsOK bool   `json:"tcp_timestamps_ok"`
	OverallStatus   string `json:"overall_status"` // "OK", "WARNING", "ERROR"
}

func RunDiagnostics(binDir string) DiagnosticReport {
	report := DiagnosticReport{
		IsAdmin:         IsAdmin(),
		WinDivertExists: true,
		TCPTimestampsOK: true,
		OverallStatus:   "OK",
	}

	if runtime.GOOS == "windows" {
		// Check WinDivert64.sys
		sysFile := filepath.Join(binDir, "WinDivert64.sys")
		if _, err := os.Stat(sysFile); os.IsNotExist(err) {
			report.WinDivertExists = false
			report.OverallStatus = "ERROR"
		}

		// Check GoodbyeDPI
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq goodbyedpi.exe").Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "goodbyedpi.exe") {
			report.GoodbyeDPIRun = true
			report.OverallStatus = "ERROR"
		}

		// Check TCP timestamps (supports English & Russian Windows)
		tsOut, err := exec.Command("netsh", "interface", "tcp", "show", "global").CombinedOutput()
		if err == nil {
			lines := strings.Split(string(tsOut), "\n")
			found := false
			for _, line := range lines {
				lower := strings.ToLower(line)
				if strings.Contains(lower, "1323") || strings.Contains(lower, "timestamp") || strings.Contains(lower, "временн") {
					found = true
					if strings.Contains(lower, "enabled") || strings.Contains(lower, "включ") || strings.Contains(lower, "разреш") {
						report.TCPTimestampsOK = true
					} else {
						report.TCPTimestampsOK = false
					}
					break
				}
			}
			if !found {
				lowerAll := strings.ToLower(string(tsOut))
				if strings.Contains(lowerAll, "1323") && (strings.Contains(lowerAll, "enabled") || strings.Contains(lowerAll, "включ") || strings.Contains(lowerAll, "разреш")) {
					report.TCPTimestampsOK = true
				} else {
					report.TCPTimestampsOK = false
				}
			}
			if !report.TCPTimestampsOK && report.OverallStatus != "ERROR" {
				report.OverallStatus = "WARNING"
			}
		}
	}

	return report
}

func FixTCPTimestamps() error {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("netsh", "interface", "tcp", "set", "global", "timestamps=enabled").CombinedOutput()
		if err != nil {
			return fmt.Errorf("ошибка netsh: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}
