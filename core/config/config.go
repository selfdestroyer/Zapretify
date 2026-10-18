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

const Version = "1.3.3"
const DefaultGithubRepo = "selfdestroyer/Zapretify"

var (
	githubRepoRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$`)
	domainRegex     = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)
)

// ValidatePortRange checks that s consists of comma-separated valid ports or ranges (e.g. "80,443,1024-65535").
func ValidatePortRange(val string) error {
	val = strings.TrimSpace(val)
	if val == "" {
		return fmt.Errorf("port specification cannot be empty")
	}
	parts := strings.Split(val, ",")
	if len(parts) > 16 {
		return fmt.Errorf("too many port ranges: maximum 16 allowed")
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return fmt.Errorf("empty port element in %q", val)
		}
		if strings.Contains(p, "-") {
			rangeParts := strings.Split(p, "-")
			if len(rangeParts) != 2 {
				return fmt.Errorf("invalid range format %q", p)
			}
			var start, end int
			if n, err := fmt.Sscanf(rangeParts[0], "%d", &start); err != nil || n != 1 || fmt.Sprintf("%d", start) != rangeParts[0] {
				return fmt.Errorf("invalid port %q", rangeParts[0])
			}
			if n, err := fmt.Sscanf(rangeParts[1], "%d", &end); err != nil || n != 1 || fmt.Sprintf("%d", end) != rangeParts[1] {
				return fmt.Errorf("invalid port %q", rangeParts[1])
			}
			if start < 1 || start > 65535 || end < 1 || end > 65535 {
				return fmt.Errorf("port out of range 1-65535 in %q", p)
			}
			if start > end {
				return fmt.Errorf("range start %d cannot be greater than end %d", start, end)
			}
		} else {
			var port int
			if n, err := fmt.Sscanf(p, "%d", &port); err != nil || n != 1 || fmt.Sprintf("%d", port) != p {
				return fmt.Errorf("invalid port %q", p)
			}
			if port < 1 || port > 65535 {
				return fmt.Errorf("port out of range 1-65535: %d", port)
			}
		}
	}
	return nil
}

// ValidateDomain checks that d is a valid hostname without scheme, port, or path.
func ValidateDomain(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return fmt.Errorf("domain cannot be empty")
	}
	if strings.Contains(d, "://") || strings.Contains(d, "/") || strings.Contains(d, ":") {
		return fmt.Errorf("invalid domain %q: scheme, port, or path not allowed", d)
	}
	if !domainRegex.MatchString(d) {
		return fmt.Errorf("invalid domain format %q", d)
	}
	return nil
}

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

	// Secret: mandatory 32-character hex string (16 bytes)
	if c.TGProxy.Secret == "" {
		return fmt.Errorf("TGProxy secret cannot be empty")
	}
	secBytes, err := hex.DecodeString(c.TGProxy.Secret)
	if err != nil || len(secBytes) != 16 {
		return fmt.Errorf("invalid TGProxy secret: must be a 32-character hex string (16 bytes)")
	}

	// CFWorkerDomain and CFUserDomain
	for _, d := range c.TGProxy.CFWorkerDomain {
		if err := ValidateDomain(d); err != nil {
			return fmt.Errorf("invalid CFWorkerDomain: %w", err)
		}
	}
	for _, d := range c.TGProxy.CFUserDomain {
		if err := ValidateDomain(d); err != nil {
			return fmt.Errorf("invalid CFUserDomain: %w", err)
		}
	}

	// GithubRepo: locked to DefaultGithubRepo
	if c.App.GithubRepo != "" && c.App.GithubRepo != DefaultGithubRepo {
		return fmt.Errorf("invalid GithubRepo %q: repository is locked to %s", c.App.GithubRepo, DefaultGithubRepo)
	}

	// GameFilterTCP and GameFilterUDP port ranges
	if c.Zapret.GameFilterTCP != "" {
		if err := ValidatePortRange(c.Zapret.GameFilterTCP); err != nil {
			return fmt.Errorf("invalid GameFilterTCP: %w", err)
		}
	}
	if c.Zapret.GameFilterUDP != "" {
		if err := ValidatePortRange(c.Zapret.GameFilterUDP); err != nil {
			return fmt.Errorf("invalid GameFilterUDP: %w", err)
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
			GithubRepo:       DefaultGithubRepo,
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
		instance.App.GithubRepo = DefaultGithubRepo
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

	needsSave := false

	// Migration and normalization from v1.2.2 and older configs:
	if c.Zapret.IPSetMode == "" {
		if c.Zapret.IPSetFilter {
			c.Zapret.IPSetMode = "loaded"
		} else {
			c.Zapret.IPSetMode = "none"
		}
		needsSave = true
	}
	if c.Zapret.GameFilterMode == "" {
		if c.Zapret.GameFilter {
			c.Zapret.GameFilterMode = "all"
		} else {
			c.Zapret.GameFilterMode = "disabled"
		}
		needsSave = true
	}
	if c.Zapret.GameFilterTCP == "" || ValidatePortRange(c.Zapret.GameFilterTCP) != nil {
		c.Zapret.GameFilterTCP = "1024-65535"
		needsSave = true
	}
	if c.Zapret.GameFilterUDP == "" || ValidatePortRange(c.Zapret.GameFilterUDP) != nil {
		c.Zapret.GameFilterUDP = "1024-65535"
		needsSave = true
	}
	if c.Zapret.Strategy == "" {
		c.Zapret.Strategy = "general"
		needsSave = true
	}
	if c.TGProxy.Port < 1024 || c.TGProxy.Port > 65535 {
		c.TGProxy.Port = 1443
		needsSave = true
	}
	// Normalize secret with length 34 and dd/ee prefix
	sec := strings.TrimSpace(c.TGProxy.Secret)
	secLower := strings.ToLower(sec)
	if len(sec) == 34 && (strings.HasPrefix(secLower, "dd") || strings.HasPrefix(secLower, "ee")) {
		c.TGProxy.Secret = sec[2:]
		needsSave = true
	}
	if c.TGProxy.Secret == "" || len(c.TGProxy.Secret) != 32 {
		newSec := make([]byte, 16)
		_, _ = rand.Read(newSec)
		c.TGProxy.Secret = hex.EncodeToString(newSec)
		needsSave = true
	}

	// Filter invalid domains
	var validWorkers []string
	for _, d := range c.TGProxy.CFWorkerDomain {
		if ValidateDomain(d) == nil {
			validWorkers = append(validWorkers, strings.TrimSpace(d))
		} else {
			needsSave = true
		}
	}
	c.TGProxy.CFWorkerDomain = validWorkers

	var validUsers []string
	for _, d := range c.TGProxy.CFUserDomain {
		if ValidateDomain(d) == nil {
			validUsers = append(validUsers, strings.TrimSpace(d))
		} else {
			needsSave = true
		}
	}
	c.TGProxy.CFUserDomain = validUsers

	if c.App.GithubRepo != DefaultGithubRepo {
		c.App.GithubRepo = DefaultGithubRepo
		needsSave = true
	}

	if needsSave {
		_ = c.saveUnlocked()
	}

	return nil
}

func (c *Config) Update(fn func(cfg *Config)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c)
	c.App.GithubRepo = DefaultGithubRepo
	if err := c.Validate(); err != nil {
		return err
	}
	return c.saveUnlocked()
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

// Clone returns a deep copy of the Config without copying the internal mutex.
func (c *Config) Clone() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()

	clone := &Config{
		App:        c.App,
		Zapret:     c.Zapret,
		TGProxy:    c.TGProxy,
		configPath: c.configPath,
	}

	if c.TGProxy.CFWorkerDomain != nil {
		clone.TGProxy.CFWorkerDomain = make([]string, len(c.TGProxy.CFWorkerDomain))
		copy(clone.TGProxy.CFWorkerDomain, c.TGProxy.CFWorkerDomain)
	}
	if c.TGProxy.CFUserDomain != nil {
		clone.TGProxy.CFUserDomain = make([]string, len(c.TGProxy.CFUserDomain))
		copy(clone.TGProxy.CFUserDomain, c.TGProxy.CFUserDomain)
	}

	return clone
}
