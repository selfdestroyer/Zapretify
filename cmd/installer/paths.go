package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// isWithin reports whether path is root itself or lies inside it (both must be absolute).
func isWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

// validateInstallPath checks a user-supplied installation folder and returns its cleaned form.
func validateInstallPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("не указан путь установки")
	}
	if !utf8.ValidString(p) || len(p) > 240 {
		return "", fmt.Errorf("недопустимый путь установки")
	}
	for _, r := range p {
		if r < 0x20 || r == '"' || r == '<' || r == '>' || r == '|' || r == '*' || r == '?' {
			return "", fmt.Errorf("путь содержит недопустимые символы")
		}
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("путь установки должен быть абсолютным")
	}
	clean := filepath.Clean(p)
	if filepath.Dir(clean) == clean {
		return "", fmt.Errorf("нельзя устанавливать в корень диска — выберите вложенную папку")
	}
	for _, env := range []string{"SystemRoot", "windir"} {
		if sys := os.Getenv(env); sys != "" && isWithin(filepath.Clean(sys), clean) {
			return "", fmt.Errorf("нельзя устанавливать в системную папку Windows")
		}
	}
	if fi, err := os.Stat(clean); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("по указанному пути находится файл, а не папка")
	}
	return clean, nil
}
