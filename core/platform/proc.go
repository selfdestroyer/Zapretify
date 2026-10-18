package platform

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SamePath reports whether two filesystem paths point to the same location.
// The comparison is case-insensitive on Windows and ignores the \\?\ long-path prefix.
func SamePath(a, b string) bool {
	norm := func(p string) string {
		p = strings.TrimPrefix(p, `\\?\`)
		p = filepath.Clean(p)
		if runtime.GOOS == "windows" {
			p = strings.ToLower(p)
		}
		return p
	}
	if a == "" || b == "" {
		return false
	}
	return norm(a) == norm(b)
}
