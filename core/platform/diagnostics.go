package platform

import (
	"fmt"
	"os"
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

	ForeignWinws []ForeignProcess `json:"foreign_winws"` // winws.exe started from other folders
	VPNAdapters  []string         `json:"vpn_adapters"`  // connected VPN / TUN adapters
	DNS          DNSCheck         `json:"dns"`
}

// warn raises the overall status to WARNING unless it already is an ERROR.
func (r *DiagnosticReport) warn() {
	if r.OverallStatus != "ERROR" {
		r.OverallStatus = "WARNING"
	}
}

func RunDiagnostics(binDir string) DiagnosticReport {
	report := DiagnosticReport{
		IsAdmin:         IsAdmin(),
		WinDivertExists: true,
		TCPTimestampsOK: true,
		OverallStatus:   "OK",
		ForeignWinws:    []ForeignProcess{},
		VPNAdapters:     []string{},
		DNS:             DNSCheck{Spoofed: []DNSSpoof{}},
	}

	// The DNS check waits on the network, so it runs alongside the local checks.
	dnsDone := make(chan DNSCheck, 1)
	go func() { dnsDone <- CheckDNS() }()

	if runtime.GOOS == "windows" {
		// Check WinDivert64.sys
		sysFile := filepath.Join(binDir, "WinDivert64.sys")
		if _, err := os.Stat(sysFile); os.IsNotExist(err) {
			report.WinDivertExists = false
			report.OverallStatus = "ERROR"
		}

		// Check GoodbyeDPI
		out, err := OutputHidden("tasklist", "/FI", "IMAGENAME eq goodbyedpi.exe")
		if err == nil && strings.Contains(strings.ToLower(string(out)), "goodbyedpi.exe") {
			report.GoodbyeDPIRun = true
			report.OverallStatus = "ERROR"
		}

		// Check TCP timestamps (supports English & Russian Windows)
		tsOut, err := OutputHidden("netsh", "interface", "tcp", "show", "global")
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
			if !report.TCPTimestampsOK {
				report.warn()
			}
		}

		// Another copy of zapret (Flowseal's bat files, an older Zapretify) intercepts the same traffic.
		if foreign := ForeignImages("winws.exe", binDir); len(foreign) > 0 {
			report.ForeignWinws = foreign
			report.OverallStatus = "ERROR"
		}
	}

	// Traffic routed into a VPN tunnel is encrypted before Zapret sees it, so the bypass has no effect on it.
	if vpn := ActiveVPNAdapters(); len(vpn) > 0 {
		report.VPNAdapters = vpn
		report.warn()
	}

	report.DNS = <-dnsDone
	if len(report.DNS.Spoofed) > 0 {
		report.warn()
	}

	return report
}

func FixTCPTimestamps() error {
	if runtime.GOOS == "windows" {
		out, err := OutputHidden("netsh", "interface", "tcp", "set", "global", "timestamps=enabled")
		if err != nil {
			return fmt.Errorf("ошибка netsh: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}
