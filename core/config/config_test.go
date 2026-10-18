package config

import (
	"os"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	cfg := DefaultConfig()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid, got: %v", err)
	}

	// Test invalid TGProxy Port
	cfg.TGProxy.Port = 80 // below 1024
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for port 80, got nil")
	}
	cfg.TGProxy.Port = 70000 // above 65535
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for port 70000, got nil")
	}
	cfg.TGProxy.Port = 1443 // restore valid

	// Test invalid TGProxy Host (non-loopback)
	cfg.TGProxy.Host = "0.0.0.0"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for non-loopback host 0.0.0.0, got nil")
	}
	cfg.TGProxy.Host = "example.com"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for non-loopback host example.com, got nil")
	}
	cfg.TGProxy.Host = "127.0.0.1" // restore valid

	// Test invalid TGProxy Secret (not hex or wrong length)
	cfg.TGProxy.Secret = "not-a-hex-string-at-all!!"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for invalid hex secret, got nil")
	}
	cfg.TGProxy.Secret = "1234abcd" // too short (not 32 hex chars)
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for short secret, got nil")
	}
	cfg.TGProxy.Secret = "0123456789abcdef0123456789abcdef" // valid 32 chars
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid secret, got error: %v", err)
	}

	// Test invalid GithubRepo (must match DefaultGithubRepo)
	cfg.App.GithubRepo = "evil-hacker/zapret"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for non-default GithubRepo, got nil")
	}
	cfg.App.GithubRepo = DefaultGithubRepo // restore valid
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid GithubRepo, got error: %v", err)
	}

	// Test invalid IPSetMode
	cfg.Zapret.IPSetMode = "unknown-mode"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for invalid IPSetMode, got nil")
	}
	cfg.Zapret.IPSetMode = "none" // restore valid

	// Test invalid GameFilterMode
	cfg.Zapret.GameFilterMode = "super-filter"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for invalid GameFilterMode, got nil")
	}
	cfg.Zapret.GameFilterMode = "disabled" // restore valid

	// Test GameFilterTCP/UDP validation
	cfg.Zapret.GameFilterTCP = "1024-65535 --malicious-arg"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for GameFilterTCP with argument injection, got nil")
	}
	cfg.Zapret.GameFilterTCP = "1024-65535" // restore valid

	// Test CFWorkerDomain validation
	cfg.TGProxy.CFWorkerDomain = []string{"https://worker.dev/bad"}
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for CFWorkerDomain with scheme and path, got nil")
	}
	cfg.TGProxy.CFWorkerDomain = []string{"worker.example.com"} // restore valid

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected config to be valid after restores, got: %v", err)
	}
}

func TestValidatePortRange(t *testing.T) {
	valid := []string{
		"80",
		"443",
		"1024-65535",
		"80,443",
		"80,443,1024-65535",
		"1-100,200-300,5000",
	}
	for _, v := range valid {
		if err := ValidatePortRange(v); err != nil {
			t.Errorf("ValidatePortRange(%q) expected nil, got: %v", v, err)
		}
	}

	invalid := []string{
		"",
		"   ",
		"0",
		"-1",
		"70000",
		"1024-65535 --foo",
		"65535-1024",
		"80,,443",
		"80-",
		"-80",
		"abc",
		"1024-abc",
	}
	for _, inv := range invalid {
		if err := ValidatePortRange(inv); err == nil {
			t.Errorf("ValidatePortRange(%q) expected error, got nil", inv)
		}
	}
}

func TestValidateDomain(t *testing.T) {
	valid := []string{
		"example.com",
		"sub.example.com",
		"my-worker.workers.dev",
		"cloudflare.com",
	}
	for _, v := range valid {
		if err := ValidateDomain(v); err != nil {
			t.Errorf("ValidateDomain(%q) expected nil, got: %v", v, err)
		}
	}

	invalid := []string{
		"",
		"https://example.com",
		"http://example.com",
		"example.com/path",
		"example.com:8080",
		"example..com",
		"bad_domain!.com",
	}
	for _, inv := range invalid {
		if err := ValidateDomain(inv); err == nil {
			t.Errorf("ValidateDomain(%q) expected error, got nil", inv)
		}
	}
}

func TestConfigMigration(t *testing.T) {
	tempDir := t.TempDir()

	// Prepare raw JSON mimicking an old v1.2.2 config with missing/invalid fields:
	// - empty ipset_mode
	// - empty game_filter_mode
	// - out-of-range port (80)
	// - 34-char secret starting with "dd"
	rawOldConfig := []byte(`{
		"app": {
			"autostart_app": true,
			"github_repo": "Flowseal/zapret-discord-youtube"
		},
		"zapret": {
			"strategy": "",
			"ipset_mode": "",
			"game_filter_mode": ""
		},
		"tg_proxy": {
			"host": "127.0.0.1",
			"port": 80,
			"secret": "dd0123456789abcdef0123456789abcdef"
		}
	}`)

	cfg := &Config{
		configPath: tempDir + "/config.json",
	}

	// Write old config file
	if err := os.WriteFile(cfg.configPath, rawOldConfig, 0644); err != nil {
		t.Fatalf("failed to write test old config: %v", err)
	}

	// Load should auto-migrate fields without error
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load should succeed with migration, got error: %v", err)
	}

	if cfg.Zapret.IPSetMode != "none" {
		t.Errorf("expected IPSetMode to be 'none', got %q", cfg.Zapret.IPSetMode)
	}
	if cfg.Zapret.GameFilterMode != "disabled" {
		t.Errorf("expected GameFilterMode to be 'disabled', got %q", cfg.Zapret.GameFilterMode)
	}
	if cfg.Zapret.Strategy != "general" {
		t.Errorf("expected Strategy to default to 'general', got %q", cfg.Zapret.Strategy)
	}
	if cfg.TGProxy.Port != 1443 {
		t.Errorf("expected TGProxy.Port to reset to 1443, got %d", cfg.TGProxy.Port)
	}
	if cfg.TGProxy.Secret != "0123456789abcdef0123456789abcdef" {
		t.Errorf("expected 34-char secret prefix 'dd' to be stripped, got %q", cfg.TGProxy.Secret)
	}
	if cfg.App.GithubRepo != "selfdestroyer/Zapretify" {
		t.Errorf("expected GithubRepo to migrate to selfdestroyer/Zapretify, got %q", cfg.App.GithubRepo)
	}

	// Verify migrated config validates cleanly
	if err := cfg.Validate(); err != nil {
		t.Errorf("migrated config failed validation: %v", err)
	}
}

func TestConfigUpdate(t *testing.T) {
	tempDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.configPath = tempDir + "/config.json"

	err := cfg.Update(func(c *Config) {
		c.Zapret.Strategy = "alt5"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if cfg.Zapret.Strategy != "alt5" {
		t.Errorf("expected strategy 'alt5', got %q", cfg.Zapret.Strategy)
	}

	// Test Update with invalid mutation returns validation error
	err = cfg.Update(func(c *Config) {
		c.TGProxy.Port = 99999
	})
	if err == nil {
		t.Errorf("expected error for invalid port in Update, got nil")
	}
}

func TestUpdateRollsBackOnValidationError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.configPath = t.TempDir() + "/config.json"

	if err := cfg.Update(func(c *Config) { c.TGProxy.Port = 99999 }); err == nil {
		t.Fatalf("expected validation error")
	}
	if cfg.TGProxy.Port != 1443 {
		t.Fatalf("live config must not keep rejected value, got port %d", cfg.TGProxy.Port)
	}
	// A following valid update must not be poisoned by the previous failed one.
	if err := cfg.Update(func(c *Config) { c.Zapret.Strategy = "alt2" }); err != nil {
		t.Fatalf("valid update after a failed one must succeed: %v", err)
	}
}

func TestUpdateRollsBackOnSaveError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.configPath = t.TempDir() + "/missing-dir/config.json" // directory does not exist -> save fails

	if err := cfg.Update(func(c *Config) { c.Zapret.Strategy = "alt3" }); err == nil {
		t.Fatalf("expected save error")
	}
	if cfg.Zapret.Strategy == "alt3" {
		t.Fatalf("in-memory config must be rolled back when saving fails")
	}
}

func TestLoadNormalizesInvalidValuesSoUpdateKeepsWorking(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{
		"zapret": {"strategy": "general", "ipset_mode": "BOGUS", "game_filter_mode": "weird"},
		"tg_proxy": {"host": "0.0.0.0", "port": 1443, "secret": "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			"cf_worker_domain": ["ok.example.com", "http://bad/"]}
	}`)
	cfg := &Config{configPath: dir + "/config.json"}
	if err := os.WriteFile(cfg.configPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("normalized config must validate: %v", err)
	}
	if cfg.TGProxy.Host != "127.0.0.1" {
		t.Errorf("non-loopback host must be reset, got %q", cfg.TGProxy.Host)
	}
	if !isValidSecretHex(cfg.TGProxy.Secret) {
		t.Errorf("non-hex secret must be regenerated, got %q", cfg.TGProxy.Secret)
	}
	if len(cfg.TGProxy.CFWorkerDomain) != 1 || cfg.TGProxy.CFWorkerDomain[0] != "ok.example.com" {
		t.Errorf("invalid cloudflare domains must be dropped, got %v", cfg.TGProxy.CFWorkerDomain)
	}
	if err := cfg.Update(func(c *Config) { c.TGProxy.Port = 2000 }); err != nil {
		t.Fatalf("Update after normalization must succeed: %v", err)
	}
}

func TestLoadKeepsSecretStartingWithDD(t *testing.T) {
	dir := t.TempDir()
	// A genuine 32-char secret that merely starts with "dd" must stay intact.
	sec := "dd23456789abcdef0123456789abcdef"
	raw := []byte(`{"tg_proxy": {"host": "127.0.0.1", "port": 1443, "secret": "` + sec + `"}}`)
	cfg := &Config{configPath: dir + "/config.json"}
	if err := os.WriteFile(cfg.configPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	if cfg.TGProxy.Secret != sec {
		t.Fatalf("secret was modified: %q", cfg.TGProxy.Secret)
	}
}

func TestLoadCorruptedConfigIsBackedUp(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{configPath: dir + "/config.json"}
	if err := os.WriteFile(cfg.configPath, []byte(`{"app": {"theme": "dark"`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load must recover from a corrupted file: %v", err)
	}
	if _, err := os.Stat(cfg.configPath + ".bad"); err != nil {
		t.Fatalf("corrupted config must be preserved as .bad: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("recovered config must be valid: %v", err)
	}
}
