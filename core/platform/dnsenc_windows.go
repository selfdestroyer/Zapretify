package platform

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// CheckEncryptedDNS looks for DNS-over-HTTPS settings of network adapters (Windows 11:
// Settings → Network → DNS server assignment → DNS over HTTPS). Windows 10 has no system DoH,
// only the browsers' own, which cannot be seen from here.
func CheckEncryptedDNS() EncryptedDNS {
	const base = `SYSTEM\CurrentControlSet\Services\Dnscache\InterfaceSpecificParameters`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return EncryptedDNS{Checked: true}
	}
	defer k.Close()
	ifaces, _ := k.ReadSubKeyNames(-1)
	for _, iface := range ifaces {
		for _, fam := range []string{"Doh", "Doh6"} {
			path := base + `\` + iface + `\DohInterfaceSettings\` + fam
			dk, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS)
			if err != nil {
				continue
			}
			servers, _ := dk.ReadSubKeyNames(-1)
			dk.Close()
			for _, srv := range servers {
				sk, err := registry.OpenKey(registry.LOCAL_MACHINE, path+`\`+srv, registry.QUERY_VALUE)
				if err != nil {
					continue
				}
				flags, _, err := sk.GetIntegerValue("DohFlags")
				sk.Close()
				if err == nil && flags != 0 {
					return EncryptedDNS{Checked: true, Enabled: true, Via: strings.TrimSpace(srv)}
				}
			}
		}
	}
	return EncryptedDNS{Checked: true}
}
