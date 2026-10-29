//go:build windows

package platform

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ActiveVPNAdapters lists connected network adapters that belong to a VPN or a TUN-mode proxy.
func ActiveVPNAdapters() []string {
	size := uint32(16 * 1024)
	var buf []byte
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_UNICAST|windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil
		}
		buf = nil
	}
	if buf == nil {
		return nil
	}

	var found []string
	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		if aa.OperStatus != windows.IfOperStatusUp {
			continue
		}
		name := windows.UTF16PtrToString(aa.FriendlyName)
		desc := windows.UTF16PtrToString(aa.Description)
		if !isVPNAdapter(name, desc) {
			continue
		}
		if desc != "" && desc != name {
			name += " (" + desc + ")"
		}
		found = append(found, name)
	}
	return found
}

// OpenNetworkSettings opens the Windows "Network & internet" settings page, where DNS is configured.
func OpenNetworkSettings() error {
	cmd := exec.Command("explorer.exe", "ms-settings:network-status")
	HideWindow(cmd)
	return cmd.Start()
}
