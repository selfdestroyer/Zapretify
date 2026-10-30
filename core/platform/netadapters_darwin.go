package platform

import (
	"os/exec"
	"strings"
)

// vpnProbes are public addresses from different /8 blocks. A VPN in TUN mode (sing-box, Hiddify,
// Happ, v2rayN…) usually keeps the default route on Wi-Fi/Ethernet and adds split routes
// (1/8, 2/7, 4/6, 8/7, …) through its utun interface, so the default route alone does not show it.
var vpnProbes = []string{"1.1.1.1", "8.8.8.8", "77.88.8.8", "142.250.0.1"}

// ActiveVPNAdapters lists tunnel interfaces that carry internet traffic, as "utun4 (description)".
func ActiveVPNAdapters() []string {
	def := routeIface("default")
	if isTunnel(def) {
		return []string{def + " (VPN: весь трафик идёт через него)"}
	}
	seen := map[string]bool{}
	var list []string
	for _, ip := range vpnProbes {
		if via := routeIface(ip); via != "" && via != def && !seen[via] {
			seen[via] = true
			list = append(list, via+" (VPN в режиме TUN)")
		}
	}
	return list
}

func routeIface(dst string) string {
	out, err := exec.Command("route", "-n", "get", dst).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "interface" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func isTunnel(name string) bool {
	return strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "ppp") || strings.HasPrefix(name, "ipsec")
}

// OpenNetworkSettings opens System Settings → Network.
func OpenNetworkSettings() error {
	return exec.Command("open", "x-apple.systempreferences:com.apple.Network-Settings.extension").Run()
}
