package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	githubRepoRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
)

type AppConfig struct {
	Theme            string `json:"theme"`
	Language         string `json:"language"`
	AutostartApp     bool   `json:"autostart_app"`
	StartMinimized   bool   `json:"start_minimized"`
	MinimizeToTray   bool   `json:"minimize_to_tray"`
	GithubRepo       string `json:"github_repo"`
	AutoUpdateApp    bool   `json:"auto_update_app"`
	AutoUpdateAssets bool   `json:"auto_update_assets"`
}

type ZapretConfig struct {
	Enabled          bool   `json:"enabled"`
	Strategy         string `json:"strategy"`
	GameFilterMode   string `json:"game_filter_mode"` // "disabled", "udp", "tcp", "all"
	GameFilterTCP    string `json:"game_filter_tcp"`  // e.g. "1024-65535"
	GameFilterUDP    string `json:"game_filter_udp"`  // e.g. "1024-65535"
	IPSetMode        string `json:"ipset_mode"`        // "loaded", "none", "any"
	AutostartService bool   `json:"autostart_service"`
	ServiceMode      bool   `json:"service_mode"`

	// Legacy compatibility flags
	GameFilter  bool `json:"game_filter"`
	IPSetFilter bool `json:"ipset_filter"`
}

type TGProxyConfig struct {
	Enabled        bool     `json:"enabled"`
	Autostart      bool     `json:"autostart"`
	Host           string   `json:"host"`
	Port           int      `json:"port"`
	Secret         string   `json:"secret"`
	CFProxy        bool     `json:"cfproxy"`
	H2             bool     `json:"h2"`
	CFWorkerDomain []string `json:"cf_worker_domain"`
	CFUserDomain   []string `json:"cf_user_domain"`
	ForceTestDC    bool     `json:"force_test_dc"`
}

type Config struct {
	App     AppConfig     `json:"app"`
	Zapret  ZapretConfig  `json:"zapret"`
	TGProxy TGProxyConfig `json:"tg_proxy"`

	mu         sync.RWMutex `json:"-"`
	configPath string       `json:"-"`
}

// Validate checks configuration fields for sanity and security.
func (c *Config) Validate() error {
	// Port range 1024-65535 for TGProxy
	if c.TGProxy.Port < 1024 || c.TGProxy.Port > 65535 {
		return fmt.Errorf("invalid TGProxy port %d: must be between 1024 and 65535", c.TGProxy.Port)
	}

	// Host: only loopback allowed (127.0.0.1, localhost, ::1, or empty)
	host := strings.TrimSpace(strings.ToLower(c.TGProxy.Host))
	if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("invalid TGProxy host %q: only loopback (127.0.0.1, localhost) is permitted", c.TGProxy.Host)
	}

	// Secret: valid 32-character hex string (16 bytes)
	if c.TGProxy.Secret != "" {
		secBytes, err := hex.DecodeString(c.TGProxy.Secret)
		if err != nil || len(secBytes) != 16 {
			return fmt.Errorf("invalid TGProxy secret: must be a 32-character hex string (16 bytes)")
		}
	}

	// GithubRepo: must follow owner/repo format
	if c.App.GithubRepo != "" {
		if !githubRepoRegex.MatchString(c.App.GithubRepo) {
			return fmt.Errorf("invalid GithubRepo %q: must match owner/repo pattern", c.App.GithubRepo)
		}
	}

	// IPSetMode: loaded, none, any
	if c.Zapret.IPSetMode != "" {
		switch strings.ToLower(c.Zapret.IPSetMode) {
		case "loaded", "none", "any":
			// valid
		default:
			return fmt.Errorf("invalid IPSetMode %q: allowed modes are 'none', 'loaded', 'any'", c.Zapret.IPSetMode)
		}
	}

	// GameFilterMode: disabled, udp, tcp, all
	if c.Zapret.GameFilterMode != "" {
		switch strings.ToLower(c.Zapret.GameFilterMode) {
		case "disabled", "udp", "tcp", "all":
			// valid
		default:
			return fmt.Errorf("invalid GameFilterMode %q: allowed modes are 'disabled', 'udp', 'tcp', 'all'", c.Zapret.GameFilterMode)
		}
	}

	return nil
}

func DefaultConfig() *Config {
	secretBytes := make([]byte, 16)
	_, _ = rand.Read(secretBytes)

	return &Config{
		App: AppConfig{
			Theme:            "dark",
			Language:         "ru",
			AutostartApp:     false,
			StartMinimized:   false,
			MinimizeToTray:   true,
			GithubRepo:       "selfdestroyer/Zapretify",
			AutoUpdateApp:    true,
			AutoUpdateAssets: true,
		},
		Zapret: ZapretConfig{
			Enabled:          false,
			Strategy:         "general",
			GameFilterMode:   "disabled",
			GameFilterTCP:    "1024-65535",
			GameFilterUDP:    "1024-65535",
			IPSetMode:        "none",
			AutostartService: false,
			ServiceMode:      false,
			GameFilter:       false,
			IPSetFilter:      false,
		},
		TGProxy: TGProxyConfig{
			Enabled:        false,
			Autostart:      false,
			Host:           "127.0.0.1",
			Port:           1443,
			Secret:         hex.EncodeToString(secretBytes),
			CFProxy:        true,
			H2:             true,
			CFWorkerDomain: []string{},
			CFUserDomain:   []string{},
			ForceTestDC:    false,
		},
	}
}

var (
	instance *Config
	once     sync.Once
)

func Get() *Config {
	once.Do(func() {
		instance = DefaultConfig()
		exePath, err := os.Executable()
		if err != nil {
			exePath = "."
		}
		appDir := filepath.Dir(exePath)
		instance.configPath = filepath.Join(appDir, "config.json")
		_ = instance.Load()
		if instance.App.GithubRepo == "" || instance.App.GithubRepo == "Flowseal/zapret-discord-youtube" {
			instance.App.GithubRepo = "selfdestroyer/Zapretify"
		}
	})
	return instance
}

func (c *Config) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.saveUnlocked()
		}
		return err
	}

	if err := json.Unmarshal(data, c); err != nil {
		return err
	}

	if c.TGProxy.Secret == "" {
		sec := make([]byte, 16)
		_, _ = rand.Read(sec)
		c.TGProxy.Secret = hex.EncodeToString(sec)
		_ = c.saveUnlocked()
	}

	return nil
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveUnlocked()
}

func (c *Config) saveUnlocked() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, c.configPath)
}
