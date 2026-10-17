package zapret

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ListManager struct {
	listsDir string
}

func NewListManager(listsDir string) *ListManager {
	return &ListManager{listsDir: listsDir}
}

func (m *ListManager) cleanPath(filename string) (string, error) {
	cleanName := filepath.Base(filepath.Clean(filename))
	if cleanName == "." || cleanName == "/" || cleanName == "\\" || strings.Contains(filename, "..") || strings.ContainsAny(filename, `/\`) {
		return "", fmt.Errorf("invalid list filename: %s", filename)
	}
	fullPath := filepath.Join(m.listsDir, cleanName)
	rel, err := filepath.Rel(m.listsDir, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path traversal detected: %s", filename)
	}
	return fullPath, nil
}

func (m *ListManager) ReadRaw(filename string) (string, error) {
	p, err := m.cleanPath(filename)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (m *ListManager) WriteRaw(filename string, content string) error {
	p, err := m.cleanPath(filename)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(m.listsDir, 0755)
	return os.WriteFile(p, []byte(content), 0644)
}

func (m *ListManager) ReadDomains(filename string) ([]string, error) {
	raw, err := m.ReadRaw(filename)
	if err != nil {
		return nil, err
	}

	var domains []string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			domains = append(domains, line)
		}
	}
	return domains, nil
}

func EnsureUserLists(listsDir string) error {
	_ = os.MkdirAll(listsDir, 0755)

	files := map[string]string{
		"ipset-exclude-user.txt": "203.0.113.113/32\n",
		"list-general-user.txt":  "# Never leave this file empty\ndomain.example.abc\n",
		"list-exclude-user.txt":  "domain.example.abc\n",
	}

	for name, defaultContent := range files {
		p := filepath.Join(listsDir, name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, []byte(defaultContent), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}
