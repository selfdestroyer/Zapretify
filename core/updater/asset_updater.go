package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"zapretify/core/logger"
)

type AssetUpdateSummary struct {
	UpdatedFiles []string `json:"updated_files"`
	FailedFiles  []string `json:"failed_files"`
	Message      string   `json:"message"`
}

var UpstreamAssetURLs = map[string]string{
	"list-general.txt":  "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-general.txt",
	"list-google.txt":   "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-google.txt",
	"list-exclude.txt":  "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-exclude.txt",
	"ipset-exclude.txt": "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/ipset-exclude.txt",
	"ipset-all.txt":     "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/ipset-all.txt",
}

var DefaultStrategyBatchFiles = []string{
	"general.bat",
	"general (ALT).bat",
	"general (ALT2).bat",
	"general (ALT3).bat",
	"general (ALT4).bat",
	"general (ALT5).bat",
	"general (ALT6).bat",
	"general (ALT7).bat",
	"general (ALT8).bat",
	"general (ALT9).bat",
	"general (ALT10).bat",
	"general (ALT11).bat",
	"general (ALT12).bat",
	"general (ALT13).bat",
	"general (EXP).bat",
	"general (FAKE TLS AUTO).bat",
	"general (FAKE TLS AUTO ALT).bat",
	"general (FAKE TLS AUTO ALT2).bat",
	"general (FAKE TLS AUTO ALT3).bat",
	"general (SIMPLE FAKE).bat",
	"general (SIMPLE FAKE ALT).bat",
	"general (SIMPLE FAKE ALT2).bat",
}

type StrategyRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RawArgs     string `json:"raw_args"`
}

type StrategiesData struct {
	Version    string           `json:"version"`
	Strategies []StrategyRecord `json:"strategies"`
}

// ParseBatRawArgs extracts the winws.exe argument chain from a Flowseal strategy .bat script.
func ParseBatRawArgs(content string) string {
	lines := strings.Split(content, "\n")
	inCmd := false
	var parts []string
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		lineClean := strings.TrimSpace(line)
		if strings.Contains(lineClean, "winws.exe\"") {
			idx := strings.Index(lineClean, "winws.exe\"") + len("winws.exe\"")
			part := strings.TrimSpace(lineClean[idx:])
			part = strings.TrimSuffix(part, "^")
			part = strings.TrimSpace(part)
			if part != "" {
				parts = append(parts, part)
			}
			inCmd = true
		} else if inCmd {
			if strings.HasSuffix(lineClean, "^") {
				part := strings.TrimSuffix(lineClean, "^")
				part = strings.TrimSpace(part)
				if part != "" {
					parts = append(parts, part)
				}
			} else {
				if lineClean != "" {
					parts = append(parts, lineClean)
				}
				break
			}
		}
	}
	return strings.Join(parts, " ")
}

func formatStrategyName(id string) string {
	if id == "general" {
		return "General"
	}
	if strings.HasPrefix(id, "general (") && strings.HasSuffix(id, ")") {
		inner := strings.TrimSuffix(strings.TrimPrefix(id, "general ("), ")")
		return fmt.Sprintf("General [%s]", inner)
	}
	return id
}

func discoverStrategyBatchFiles(client *http.Client) []string {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/Flowseal/zapret-discord-youtube/contents/", nil)
	if err == nil {
		req.Header.Set("User-Agent", "Zapretify-Updater/"+CurrentVersion)
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			var items []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&items); err == nil {
				seen := make(map[string]bool)
				var discovered []string
				for _, item := range items {
					if strings.HasSuffix(item.Name, ".bat") && strings.HasPrefix(item.Name, "general") {
						seen[item.Name] = true
						discovered = append(discovered, item.Name)
					}
				}
				for _, df := range DefaultStrategyBatchFiles {
					if !seen[df] {
						discovered = append(discovered, df)
					}
				}
				if len(discovered) > 0 {
					return discovered
				}
			}
		}
	}
	return DefaultStrategyBatchFiles
}

func UpdateUpstreamAssets(listsDir string, appDir string) (*AssetUpdateSummary, error) {
	summary := &AssetUpdateSummary{}
	client := &http.Client{Timeout: 10 * time.Second}

	logger.Log("[Updater] Запуск синхронизации хост-листов и стратегий из upstream Flowseal...")

	_ = os.MkdirAll(listsDir, 0755)

	// 1. Sync host lists
	for filename, fileURL := range UpstreamAssetURLs {
		resp, err := client.Get(fileURL)
		if err != nil || resp.StatusCode != http.StatusOK {
			summary.FailedFiles = append(summary.FailedFiles, filename)
			if resp != nil {
				_ = resp.Body.Close()
			}
			continue
		}

		targetPath := filepath.Join(listsDir, filename)
		tmpPath := targetPath + ".download"

		out, err := os.Create(tmpPath)
		if err != nil {
			_ = resp.Body.Close()
			summary.FailedFiles = append(summary.FailedFiles, filename)
			continue
		}

		_, err = io.Copy(out, resp.Body)
		_ = out.Close()
		_ = resp.Body.Close()

		if err != nil {
			_ = os.Remove(tmpPath)
			summary.FailedFiles = append(summary.FailedFiles, filename)
			continue
		}

		_ = os.Rename(tmpPath, targetPath)
		summary.UpdatedFiles = append(summary.UpdatedFiles, filename)
	}

	// 2. Sync strategies from Flowseal .bat files
	batchFiles := discoverStrategyBatchFiles(client)
	var (
		stratMu       sync.Mutex
		parsedResults = make(map[string]string)
	)

	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)

	for _, bf := range batchFiles {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			rawURL := "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/" + url.PathEscape(filename)
			resp, err := client.Get(rawURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil {
					_ = resp.Body.Close()
				}
				stratMu.Lock()
				summary.FailedFiles = append(summary.FailedFiles, filename)
				stratMu.Unlock()
				return
			}

			bodyBytes, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				stratMu.Lock()
				summary.FailedFiles = append(summary.FailedFiles, filename)
				stratMu.Unlock()
				return
			}

			parsedArgs := ParseBatRawArgs(string(bodyBytes))
			if parsedArgs != "" && strings.Contains(parsedArgs, "--wf-") {
				id := strings.TrimSuffix(filename, ".bat")
				stratMu.Lock()
				parsedResults[id] = parsedArgs
				stratMu.Unlock()
			}
		}(bf)
	}
	wg.Wait()

	// 3. Update strategies.json
	if len(parsedResults) > 0 {
		strategiesPath := filepath.Join(appDir, "strategies.json")
		data, err := os.ReadFile(strategiesPath)
		if err != nil {
			data, err = os.ReadFile("strategies.json")
			if err == nil {
				strategiesPath = "strategies.json"
			}
		}

		var fileData StrategiesData
		if err == nil {
			_ = json.Unmarshal(data, &fileData)
		}
		if fileData.Version == "" {
			fileData.Version = "1.10.3"
		}

		stratIndex := make(map[string]int)
		for i, s := range fileData.Strategies {
			stratIndex[s.ID] = i
		}

		for id, rawArgs := range parsedResults {
			if idx, ok := stratIndex[id]; ok {
				fileData.Strategies[idx].RawArgs = rawArgs
			} else {
				fileData.Strategies = append(fileData.Strategies, StrategyRecord{
					ID:          id,
					Name:        formatStrategyName(id),
					Description: fmt.Sprintf("Стратегия Flowseal (%s)", id),
					RawArgs:     rawArgs,
				})
				stratIndex[id] = len(fileData.Strategies) - 1
			}
		}

		outJSON, err := json.MarshalIndent(fileData, "", "  ")
		if err == nil {
			tmpFile := strategiesPath + ".tmp"
			if err := os.WriteFile(tmpFile, outJSON, 0644); err == nil {
				_ = os.Rename(tmpFile, strategiesPath)
				summary.UpdatedFiles = append(summary.UpdatedFiles, "strategies.json")
				if appDir != "" && appDir != "." {
					if _, err := os.Stat("strategies.json"); err == nil {
						_ = os.WriteFile("strategies.json", outJSON, 0644)
					}
				}
			}
		}
	}

	listsCount := 0
	for _, fn := range summary.UpdatedFiles {
		if strings.HasPrefix(fn, "list-") || strings.HasPrefix(fn, "ipset-") {
			listsCount++
		}
	}

	summary.Message = fmt.Sprintf("Списков обновлено: %d/%d, Стратегий обновлено: %d (Ошибок: %d)",
		listsCount, len(UpstreamAssetURLs), len(parsedResults), len(summary.FailedFiles))
	logger.Log("[Updater] Синхронизация завершена. %s", summary.Message)

	return summary, nil
}
