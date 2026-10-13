package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const CurrentVersion = "1.2.2"

type ReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type GithubRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt string         `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

type UpdateCheckResult struct {
	Available      bool           `json:"available"`
	CurrentVersion string         `json:"current_version"`
	LatestVersion  string         `json:"latest_version"`
	ReleaseNotes   string         `json:"release_notes"`
	DownloadURL    string         `json:"download_url"`
	ReleaseURL     string         `json:"release_url"`
	AssetSize      int64          `json:"asset_size"`
	Release        *GithubRelease `json:"-"`
}

func parseSemVer(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	res := make([]int, 0, len(parts))
	for _, p := range parts {
		if idx := strings.IndexAny(p, "-+"); idx != -1 {
			p = p[:idx]
		}
		var num int
		_, _ = fmt.Sscanf(p, "%d", &num)
		res = append(res, num)
	}
	for len(res) < 3 {
		res = append(res, 0)
	}
	return res
}

func isVersionNewer(latest, current string) bool {
	l := parseSemVer(latest)
	c := parseSemVer(current)
	for i := 0; i < len(l) && i < len(c); i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	return false
}

func CheckAppUpdate(repo string) (*UpdateCheckResult, error) {
	if repo == "" || repo == "Flowseal/zapret-discord-youtube" {
		repo = "selfdestroyer/Zapretify"
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Zapretify-Updater/"+CurrentVersion)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status: %d", resp.StatusCode)
	}

	var rel GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}

	isNewer := isVersionNewer(rel.TagName, CurrentVersion)

	downloadURL := ""
	var assetSize int64
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".exe") || strings.HasSuffix(strings.ToLower(a.Name), ".zip") {
			downloadURL = a.BrowserDownloadURL
			assetSize = a.Size
			break
		}
	}

	releaseURL := rel.HTMLURL
	if releaseURL == "" {
		releaseURL = fmt.Sprintf("https://github.com/%s/releases", repo)
	}

	return &UpdateCheckResult{
		Available:      isNewer,
		CurrentVersion: CurrentVersion,
		LatestVersion:  rel.TagName,
		ReleaseNotes:   rel.Body,
		DownloadURL:    downloadURL,
		ReleaseURL:     releaseURL,
		AssetSize:      assetSize,
		Release:        &rel,
	}, nil
}

func PerformInPlaceUpdate(downloadURL string) error {
	if downloadURL == "" {
		return fmt.Errorf("download URL is empty")
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	tmpPath := exePath + ".new"
	oldPath := exePath + ".old"

	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	_ = out.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Rename current exe to .old, and new to current
	_ = os.Remove(oldPath)
	if err := os.Rename(exePath, oldPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rotate running executable: %w", err)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		_ = os.Rename(oldPath, exePath) // rollback
		return fmt.Errorf("failed to move new executable: %w", err)
	}

	// Restart new process and exit current
	cmd := exec.Command(exePath)
	cmd.Dir = filepath.Dir(exePath)
	_ = cmd.Start()
	os.Exit(0)
	return nil
}
