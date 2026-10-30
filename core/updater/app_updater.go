package updater

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"zapretify/core/config"
)

const CurrentVersion = config.Version

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

// pickAsset chooses the download for this platform from a release that carries both versions:
// macOS takes the .dmg; Windows takes the installer/portable (.exe/.zip) and skips files named macOS.
func pickAsset(assets []ReleaseAsset, goos string) *ReleaseAsset {
	for i := range assets {
		name := strings.ToLower(assets[i].Name)
		mac := strings.Contains(name, "macos") || strings.HasSuffix(name, ".dmg")
		switch {
		case goos == "darwin" && strings.HasSuffix(name, ".dmg"):
			return &assets[i]
		case goos != "darwin" && !mac && (strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".zip")):
			return &assets[i]
		}
	}
	return nil
}

func CheckAppUpdate(_ string) (*UpdateCheckResult, error) {
	repo := config.DefaultGithubRepo

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
	if a := pickAsset(rel.Assets, runtime.GOOS); a != nil {
		downloadURL = a.BrowserDownloadURL
		assetSize = a.Size
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
