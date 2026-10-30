package platform

import (
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
)

var dnsProtocolRe = regexp.MustCompile(`DNSProtocol = (HTTPS|TLS);`)

// CheckEncryptedDNS looks for an installed configuration profile with a DNS settings payload
// (com.apple.dnsSettings.managed): that is how macOS enables system-wide DoH/DoT.
func CheckEncryptedDNS() EncryptedDNS {
	out, err := exec.Command("system_profiler", "-json", "SPConfigurationProfileDataType").Output()
	if err != nil {
		return EncryptedDNS{}
	}
	var root any
	if json.Unmarshal(out, &root) != nil {
		return EncryptedDNS{}
	}
	res := EncryptedDNS{Checked: true}
	var walk func(v any, profile string)
	walk = func(v any, profile string) {
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				walk(x, profile)
			}
		case map[string]any:
			name, _ := t["_name"].(string)
			if _, isProfile := t["spconfigprofile_profile_identifier"]; isProfile {
				profile = name
			}
			if strings.HasPrefix(name, "com.apple.dnsSettings") {
				data, _ := t["spconfigprofile_payload_data"].(string)
				if m := dnsProtocolRe.FindStringSubmatch(data); m != nil && !res.Enabled {
					res.Enabled = true
					res.Via = profile
					if res.Via == "" {
						res.Via = "DNS over " + m[1]
					}
				}
			}
			for _, x := range t {
				walk(x, profile)
			}
		}
	}
	walk(root, "")
	return res
}
