package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The bases replaced by the last synchronisation that changed something are kept in
// <appDir>/assets-backup, so a broken upstream update can be rolled back from the UI.
// User lists (*-user.txt) are never touched by the updater and are not part of the snapshot.
const (
	assetsBackupDir     = "assets-backup"
	assetsBackupMeta    = "backup.json"
	assetsBackupStaging = assetsBackupDir + ".new"
)

// AssetsBackupInfo describes the saved snapshot of the bases.
type AssetsBackupInfo struct {
	Exists  bool      `json:"exists"`
	SavedAt time.Time `json:"saved_at"`
	Version string    `json:"version"` // Flowseal strategies version inside the snapshot
	Files   []string  `json:"files"`   // snapshot paths: lists/<name>, bin/<name>, strategies.json
}

// assetPaths maps snapshot paths of every file the updater may rewrite to their live locations.
func assetPaths(listsDir, appDir string) map[string]string {
	paths := map[string]string{"strategies.json": filepath.Join(appDir, "strategies.json")}
	for name := range UpstreamAssetURLs {
		paths["lists/"+name] = filepath.Join(listsDir, name)
	}
	binDir := filepath.Join(appDir, "bin")
	if entries, err := os.ReadDir(binDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && validBinName(e.Name()) {
				paths["bin/"+e.Name()] = filepath.Join(binDir, e.Name())
			}
		}
	}
	return paths
}

func strategiesVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var f struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &f)
	return f.Version
}

// PrepareAssetsBackup copies the current bases into a staging folder before a synchronisation.
// The caller keeps it with CommitAssetsBackup when the synchronisation wrote files, or drops it
// with DiscardAssetsBackup: a run that changed nothing must not replace the previous snapshot.
func PrepareAssetsBackup(listsDir, appDir string) (string, error) {
	staging := filepath.Join(appDir, assetsBackupStaging)
	if err := os.RemoveAll(staging); err != nil {
		return "", err
	}
	info := AssetsBackupInfo{SavedAt: time.Now(), Version: strategiesVersion(filepath.Join(appDir, "strategies.json"))}
	for rel, src := range assetPaths(listsDir, appDir) {
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			os.RemoveAll(staging)
			return "", fmt.Errorf("%s: %w", rel, err)
		}
		dst := filepath.Join(staging, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			os.RemoveAll(staging)
			return "", err
		}
		info.Files = append(info.Files, rel)
	}
	sort.Strings(info.Files)
	meta, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(staging, assetsBackupMeta), meta, 0644); err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	return staging, nil
}

// CommitAssetsBackup makes the staging folder the saved snapshot, replacing the previous one.
func CommitAssetsBackup(appDir, staging string) error {
	final := filepath.Join(appDir, assetsBackupDir)
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	return os.Rename(staging, final)
}

// DiscardAssetsBackup removes an unused staging folder.
func DiscardAssetsBackup(staging string) {
	if staging != "" {
		_ = os.RemoveAll(staging)
	}
}

// ReadAssetsBackup returns the saved snapshot, if there is one.
func ReadAssetsBackup(appDir string) AssetsBackupInfo {
	data, err := os.ReadFile(filepath.Join(appDir, assetsBackupDir, assetsBackupMeta))
	if err != nil {
		return AssetsBackupInfo{}
	}
	var info AssetsBackupInfo
	if json.Unmarshal(data, &info) != nil || len(info.Files) == 0 {
		return AssetsBackupInfo{}
	}
	info.Exists = true
	return info
}

// RestoreAssetsBackup puts the saved bases back. The bases being replaced become the new
// snapshot, so a rollback can itself be undone. It returns the snapshot that was restored.
func RestoreAssetsBackup(listsDir, appDir string) (AssetsBackupInfo, error) {
	saved := ReadAssetsBackup(appDir)
	if !saved.Exists {
		return saved, fmt.Errorf("сохранённых баз нет")
	}
	live := assetPaths(listsDir, appDir)
	backupRoot := filepath.Join(appDir, assetsBackupDir)

	// Read the whole snapshot first, so a damaged backup changes nothing.
	contents := make(map[string][]byte, len(saved.Files))
	for _, rel := range saved.Files {
		dst, ok := live[rel]
		if !ok {
			if !strings.HasPrefix(rel, "bin/") || !validBinName(strings.TrimPrefix(rel, "bin/")) {
				return saved, fmt.Errorf("недопустимый файл в резервной копии: %s", rel)
			}
			dst = filepath.Join(appDir, "bin", strings.TrimPrefix(rel, "bin/")) // fake removed since the snapshot
			live[rel] = dst
		}
		data, err := os.ReadFile(filepath.Join(backupRoot, filepath.FromSlash(rel)))
		if err != nil {
			return saved, fmt.Errorf("резервная копия повреждена (%s): %w", rel, err)
		}
		contents[rel] = data
	}

	staging, err := PrepareAssetsBackup(listsDir, appDir)
	if err != nil {
		return saved, fmt.Errorf("не удалось сохранить текущие базы: %w", err)
	}
	for _, rel := range saved.Files {
		if err := writeFileAtomic(live[rel], contents[rel], 0644); err != nil {
			DiscardAssetsBackup(staging)
			return saved, fmt.Errorf("%s: %w", rel, err)
		}
	}
	if err := CommitAssetsBackup(appDir, staging); err != nil {
		return saved, fmt.Errorf("базы восстановлены, но текущая версия не сохранена: %w", err)
	}
	return saved, nil
}
