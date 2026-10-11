package zapret

import (
	"bufio"
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

func (m *ListManager) ReadRaw(filename string) (string, error) {
	p := filepath.Join(m.listsDir, filename)
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
	p := filepath.Join(m.listsDir, filename)
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
