package zapret

import (
	"os"
	"path/filepath"
	"strings"
)

// IPSet modes:
// "none"   - ipset-all.txt contains dummy "203.0.113.113/32" (effectively bypassed/disabled)
// "loaded" - ipset-all.txt contains full list of blocked IP ranges
// "any"    - ipset-all.txt is empty (matches any IP)

func GetIPSetMode(listsDir string) string {
	p := filepath.Join(listsDir, "ipset-all.txt")
	data, err := os.ReadFile(p)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return "any"
	}
	if strings.Contains(string(data), "203.0.113.113/32") {
		return "none"
	}
	return "loaded"
}

func SetIPSetMode(listsDir string, mode string) error {
	p := filepath.Join(listsDir, "ipset-all.txt")
	backup := filepath.Join(listsDir, "ipset-all.txt.backup")

	switch strings.ToLower(mode) {
	case "loaded":
		if data, err := os.ReadFile(backup); err == nil && len(data) > 0 {
			return os.WriteFile(p, data, 0644)
		}
		return nil

	case "any":
		if curr, err := os.ReadFile(p); err == nil && !strings.Contains(string(curr), "203.0.113.113") && len(curr) > 0 {
			_ = os.WriteFile(backup, curr, 0644)
		}
		return os.WriteFile(p, []byte(""), 0644)

	case "none":
		fallthrough
	default:
		if curr, err := os.ReadFile(p); err == nil && !strings.Contains(string(curr), "203.0.113.113") && len(curr) > 0 {
			_ = os.WriteFile(backup, curr, 0644)
		}
		return os.WriteFile(p, []byte("203.0.113.113/32\n"), 0644)
	}
}
