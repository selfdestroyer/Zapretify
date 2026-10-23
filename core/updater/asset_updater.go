package updater

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"zapretify/core/logger"
)

type AssetUpdateSummary struct {
	UpdatedFiles      []string `json:"updated_files"`
	FailedFiles       []string `json:"failed_files"`
	AddedStrategies   []string `json:"added_strategies"`
	UpdatedStrategies []string `json:"updated_strategies"`
	// Changes holds one human-readable line per real change; empty when everything was already up to date.
	Changes []string `json:"changes"`
	Changed bool     `json:"changed"`
	Message string   `json:"message"`
}

// listTitles gives the host lists readable names for the sync report.
var listTitles = map[string]string{
	"list-general.txt":     "основной доменный лист",
	"list-google.txt":      "доменный лист Google",
	"list-exclude.txt":     "лист исключений доменов",
	"ipset-exclude.txt":    "лист исключений IP",
	"ipset-all.txt.backup": "IP-лист IPSet",
}

// ipsetPlaceholder is what SetIPSetMode writes into ipset-all.txt in "none" mode.
// Flowseal keeps the same placeholder in lists/ipset-all.txt, so a real list never contains it.
const ipsetPlaceholder = "203.0.113.113"

// Flowseal repository endpoints; variables so tests can point them at a local server.
var (
	flowsealRawBase = "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/"
	flowsealAPIBase = "https://api.github.com/repos/Flowseal/zapret-discord-youtube/contents/"
)

var UpstreamAssetURLs = map[string]string{
	"list-general.txt":  "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-general.txt",
	"list-google.txt":   "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-google.txt",
	"list-exclude.txt":  "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/list-exclude.txt",
	"ipset-exclude.txt": "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/lists/ipset-exclude.txt",
	// Flowseal's lists/ipset-all.txt is only the IPSet placeholder. The real list is published in
	// .service/ipset-service.txt (what service.bat "Update IPSet" downloads); it is kept as
	// ipset-all.txt.backup, which SetIPSetMode copies into ipset-all.txt in "loaded" mode.
	"ipset-all.txt.backup": "https://raw.githubusercontent.com/Flowseal/zapret-discord-youtube/main/.service/ipset-service.txt",
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

const (
	maxListBytes    = 8 << 20 // host lists / ipsets
	maxBatchBytes   = 256 << 10
	maxBinBytes     = 64 << 10 // fake payloads (bin/*.bin)
	maxVersionBytes = 64
)

// fetchLimited downloads a URL and refuses non-200 answers, empty bodies and bodies over the limit.
func fetchLimited(client *http.Client, rawURL string, limit int64) ([]byte, error) {
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response larger than %d bytes", limit)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	return body, nil
}

// validRawArgs rejects argument chains that contain control characters or newlines.
func validRawArgs(args string) bool {
	for _, r := range args {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// writeFileAtomic writes data next to the target and renames it into place.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".download"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// listEntries returns the meaningful (non-empty, non-comment) lines of a host list.
func listEntries(data []byte) map[string]struct{} {
	set := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[line] = struct{}{}
	}
	return set
}

// diffListEntries counts entries added to and removed from a host list.
func diffListEntries(oldData, newData []byte) (added, removed int) {
	oldSet, newSet := listEntries(oldData), listEntries(newData)
	for e := range newSet {
		if _, ok := oldSet[e]; !ok {
			added++
		}
	}
	for e := range oldSet {
		if _, ok := newSet[e]; !ok {
			removed++
		}
	}
	return added, removed
}

// currentListContent returns the local copy of a host list and whether it exists.
func currentListContent(listsDir, filename string) ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(listsDir, filename))
	return data, err == nil
}

func describeListChange(filename string, existed bool, oldData, newData []byte) string {
	title := listTitles[filename]
	if title == "" {
		title = filename
	}
	if !existed {
		return fmt.Sprintf("Загружен %s (%s)", title, filename)
	}
	added, removed := diffListEntries(oldData, newData)
	var parts []string // the caller skips files whose entries did not change
	if added > 0 {
		parts = append(parts, fmt.Sprintf("+%d", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("−%d", removed))
	}
	return fmt.Sprintf("Обновлён %s (%s): %s %s", title, filename, strings.Join(parts, " / "),
		pluralRu(added+removed, "запись", "записи", "записей"))
}

func pluralRu(n int, one, few, many string) string {
	n %= 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// gitBlobSHA returns the git object id of data, the same value GitHub reports as "sha" for a file.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

var binRefPattern = regexp.MustCompile(`%BIN%([^"\s]+)`)

// referencedBins lists the bin/ files a strategy argument chain points to.
func referencedBins(rawArgs string) []string {
	var names []string
	for _, m := range binRefPattern.FindAllStringSubmatch(rawArgs, -1) {
		names = append(names, m[1])
	}
	return names
}

// validBinName accepts plain *.bin file names only: fake payloads, never executables or paths.
func validBinName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".bin") && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:`) && !strings.Contains(name, "..")
}

type upstreamBin struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// listUpstreamBins asks the GitHub API for the files in Flowseal's bin/ directory.
func listUpstreamBins(client *http.Client) ([]upstreamBin, error) {
	req, err := http.NewRequest("GET", flowsealAPIBase+"bin", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Zapretify-Updater/"+CurrentVersion)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	var items []upstreamBin
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&items); err != nil {
		return nil, err
	}
	var bins []upstreamBin
	for _, it := range items {
		if validBinName(it.Name) && it.Size > 0 && it.Size <= maxBinBytes {
			bins = append(bins, it)
		}
	}
	return bins, nil
}

// syncFakeBins brings bin/*.bin fake payloads in line with Flowseal. With the GitHub API it adds
// missing files and replaces changed ones, checking each download against the git blob sha.
// Without the API it only fetches the files the strategies need and the user does not have.
func syncFakeBins(client *http.Client, binDir string, needed map[string]bool, summary *AssetUpdateSummary) (added, updated []string) {
	_ = os.MkdirAll(binDir, 0755)
	type target struct {
		name, sha string
	}
	var targets []target
	if bins, err := listUpstreamBins(client); err == nil {
		for _, b := range bins {
			targets = append(targets, target{b.Name, b.SHA})
		}
	} else {
		logger.Log("[Updater] Список bin/ из GitHub API недоступен (%v), докачиваю только недостающие файлы.", err)
		for name := range needed {
			if _, statErr := os.Stat(filepath.Join(binDir, name)); statErr != nil {
				targets = append(targets, target{name, ""})
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].name < targets[j].name })

	for _, t := range targets {
		local, readErr := os.ReadFile(filepath.Join(binDir, t.name))
		existed := readErr == nil
		if existed && (t.sha == "" || gitBlobSHA(local) == t.sha) {
			continue
		}
		body, err := fetchLimited(client, flowsealRawBase+"bin/"+url.PathEscape(t.name), maxBinBytes)
		if err == nil && t.sha != "" && gitBlobSHA(body) != t.sha {
			err = fmt.Errorf("контрольная сумма не совпала с GitHub")
		}
		if err == nil {
			err = writeFileAtomic(filepath.Join(binDir, t.name), body, 0644)
		}
		if err != nil {
			logger.Log("[Updater] bin/%s: %v", t.name, err)
			summary.FailedFiles = append(summary.FailedFiles, "bin/"+t.name)
			continue
		}
		summary.UpdatedFiles = append(summary.UpdatedFiles, "bin/"+t.name)
		if existed {
			updated = append(updated, t.name)
		} else {
			added = append(added, t.name)
		}
	}
	return added, updated
}

var flowsealVersionPattern = regexp.MustCompile(`^\d+(\.\d+){1,3}$`)

// fetchFlowsealVersion reads the release number Flowseal publishes in .service/version.txt.
func fetchFlowsealVersion(client *http.Client) string {
	body, err := fetchLimited(client, flowsealRawBase+".service/version.txt", maxVersionBytes)
	if err != nil {
		logger.Log("[Updater] Версия Flowseal недоступна: %v", err)
		return ""
	}
	v := strings.TrimSpace(string(body))
	if !flowsealVersionPattern.MatchString(v) {
		return ""
	}
	return v
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

	// 1. Sync host lists (user-owned *-user.txt files are never part of UpstreamAssetURLs).
	// Files are rewritten only when upstream content differs from the local copy.
	listNames := make([]string, 0, len(UpstreamAssetURLs))
	for filename := range UpstreamAssetURLs {
		listNames = append(listNames, filename)
	}
	sort.Strings(listNames)
	var listChanges []string
	for _, filename := range listNames {
		body, err := fetchLimited(client, UpstreamAssetURLs[filename], maxListBytes)
		if err != nil {
			logger.Log("[Updater] %s: %v", filename, err)
			summary.FailedFiles = append(summary.FailedFiles, filename)
			continue
		}
		if filename == "ipset-all.txt.backup" && bytes.Contains(body, []byte(ipsetPlaceholder)) {
			logger.Log("[Updater] %s: upstream отдал заглушку вместо IP-листа", filename)
			summary.FailedFiles = append(summary.FailedFiles, filename)
			continue
		}
		oldData, existed := currentListContent(listsDir, filename)
		if existed && bytes.Equal(oldData, body) {
			continue
		}
		if err := writeFileAtomic(filepath.Join(listsDir, filename), body, 0644); err != nil {
			logger.Log("[Updater] %s: %v", filename, err)
			summary.FailedFiles = append(summary.FailedFiles, filename)
			continue
		}
		summary.UpdatedFiles = append(summary.UpdatedFiles, filename)
		if existed {
			if added, removed := diffListEntries(oldData, body); added == 0 && removed == 0 {
				continue // only line endings, comments or order differ: nothing worth reporting
			}
		}
		listChanges = append(listChanges, describeListChange(filename, existed, oldData, body))
	}

	// 2. Sync strategies from Flowseal .bat files
	batchFiles := discoverStrategyBatchFiles(client)
	var (
		stratMu       sync.Mutex
		parsedResults = make(map[string]string)
		batFailed     bool
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
			bodyBytes, err := fetchLimited(client, rawURL, maxBatchBytes)
			if err != nil {
				stratMu.Lock()
				summary.FailedFiles = append(summary.FailedFiles, filename)
				batFailed = true
				stratMu.Unlock()
				return
			}

			parsedArgs := ParseBatRawArgs(string(bodyBytes))
			if parsedArgs != "" && strings.Contains(parsedArgs, "--wf-") && validRawArgs(parsedArgs) {
				id := strings.TrimSuffix(filename, ".bat")
				stratMu.Lock()
				parsedResults[id] = parsedArgs
				stratMu.Unlock()
			}
		}(bf)
	}
	wg.Wait()

	// 3. Sync the fake payloads the strategies refer to (bin/*.bin)
	binDir := filepath.Join(appDir, "bin")
	needed := make(map[string]bool)
	for _, args := range parsedResults {
		for _, name := range referencedBins(args) {
			if validBinName(name) {
				needed[name] = true
			}
		}
	}
	addedBins, updatedBins := syncFakeBins(client, binDir, needed, summary)

	// A strategy that points to a payload the user still lacks would stop winws from starting:
	// keep the previous version of such a strategy instead.
	var skippedStrategies []string
	for id, args := range parsedResults {
		for _, name := range referencedBins(args) {
			if _, err := os.Stat(filepath.Join(binDir, name)); err != nil {
				logger.Log("[Updater] %s пропущена: нет файла bin/%s", id, name)
				skippedStrategies = append(skippedStrategies, formatStrategyName(id))
				summary.FailedFiles = append(summary.FailedFiles, id+".bat")
				delete(parsedResults, id)
				break
			}
		}
	}
	sort.Strings(skippedStrategies)

	// 4. Update strategies.json, only when a strategy or the Flowseal version changed
	upstreamVersion := ""
	if !batFailed && len(skippedStrategies) == 0 {
		upstreamVersion = fetchFlowsealVersion(client)
	}
	var (
		fileData       StrategiesData
		versionChanged string
	)
	if len(parsedResults) > 0 {
		strategiesPath := filepath.Join(appDir, "strategies.json")
		if data, err := os.ReadFile(strategiesPath); err == nil {
			if err := json.Unmarshal(data, &fileData); err != nil {
				logger.Log("[Updater] strategies.json повреждён (%v), будет пересоздан.", err)
				fileData = StrategiesData{}
			}
		}
		if upstreamVersion != "" && upstreamVersion != fileData.Version {
			if fileData.Version != "" {
				versionChanged = fmt.Sprintf("Стратегии Flowseal: %s → %s", fileData.Version, upstreamVersion)
			} else {
				versionChanged = "Стратегии Flowseal: версия " + upstreamVersion
			}
			fileData.Version = upstreamVersion
		}

		stratIndex := make(map[string]int)
		for i, s := range fileData.Strategies {
			stratIndex[s.ID] = i
		}

		ids := make([]string, 0, len(parsedResults))
		for id := range parsedResults {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			rawArgs := parsedResults[id]
			if idx, ok := stratIndex[id]; ok {
				if fileData.Strategies[idx].RawArgs != rawArgs {
					fileData.Strategies[idx].RawArgs = rawArgs
					summary.UpdatedStrategies = append(summary.UpdatedStrategies, fileData.Strategies[idx].Name)
				}
			} else {
				fileData.Strategies = append(fileData.Strategies, StrategyRecord{
					ID:          id,
					Name:        formatStrategyName(id),
					Description: fmt.Sprintf("Стратегия Flowseal (%s)", id),
					RawArgs:     rawArgs,
				})
				stratIndex[id] = len(fileData.Strategies) - 1
				summary.AddedStrategies = append(summary.AddedStrategies, formatStrategyName(id))
			}
		}
	}

	if len(summary.AddedStrategies) > 0 || len(summary.UpdatedStrategies) > 0 || versionChanged != "" {
		strategiesPath := filepath.Join(appDir, "strategies.json")
		outJSON, err := json.MarshalIndent(fileData, "", "  ")
		if err == nil {
			err = writeFileAtomic(strategiesPath, outJSON, 0644)
		}
		if err != nil {
			logger.Log("[Updater] Не удалось сохранить strategies.json: %v", err)
			summary.FailedFiles = append(summary.FailedFiles, "strategies.json")
		} else {
			summary.UpdatedFiles = append(summary.UpdatedFiles, "strategies.json")
		}
	}

	if versionChanged != "" {
		summary.Changes = append(summary.Changes, versionChanged)
	}
	if n := len(summary.AddedStrategies); n > 0 {
		summary.Changes = append(summary.Changes, fmt.Sprintf("Добавлено %d %s: %s",
			n, pluralRu(n, "стратегия", "стратегии", "стратегий"), strings.Join(summary.AddedStrategies, ", ")))
	}
	if n := len(summary.UpdatedStrategies); n > 0 {
		summary.Changes = append(summary.Changes, fmt.Sprintf("Обновлено %d %s: %s",
			n, pluralRu(n, "стратегия", "стратегии", "стратегий"), strings.Join(summary.UpdatedStrategies, ", ")))
	}
	if n := len(skippedStrategies); n > 0 {
		summary.Changes = append(summary.Changes, fmt.Sprintf("Не обновлено %d %s (не скачались нужные файлы bin): %s",
			n, pluralRu(n, "стратегия", "стратегии", "стратегий"), strings.Join(skippedStrategies, ", ")))
	}
	if n := len(addedBins); n > 0 {
		summary.Changes = append(summary.Changes, fmt.Sprintf("Добавлено %d %s фейков: %s",
			n, pluralRu(n, "файл", "файла", "файлов"), strings.Join(addedBins, ", ")))
	}
	if n := len(updatedBins); n > 0 {
		summary.Changes = append(summary.Changes, fmt.Sprintf("Обновлено %d %s фейков: %s",
			n, pluralRu(n, "файл", "файла", "файлов"), strings.Join(updatedBins, ", ")))
	}
	summary.Changes = append(summary.Changes, listChanges...)
	summary.Changed = len(summary.Changes) > 0

	switch {
	case summary.Changed:
		summary.Message = strings.Join(summary.Changes, "\n")
	case len(summary.FailedFiles) == 0:
		summary.Message = "Изменений нет — списки и стратегии уже актуальны"
	default:
		summary.Message = "Изменений в скачанных файлах нет"
	}

	logger.Log("[Updater] Синхронизация завершена. %s (ошибок: %d)", strings.ReplaceAll(summary.Message, "\n", "; "), len(summary.FailedFiles))

	return summary, nil
}
