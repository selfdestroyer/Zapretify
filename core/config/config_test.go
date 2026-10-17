package config

import (
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

	// Test invalid GithubRepo
	cfg.App.GithubRepo = "invalid-repo-without-slash"
	if err := cfg.Validate(); err == nil {
		t.Errorf("expected error for GithubRepo without slash, got nil")
	}
	cfg.App.GithubRepo = "owner/repo" // restore valid
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

	if err := cfg.Validate(); err != nil {
		t.Errorf("expected config to be valid after restores, got: %v", err)
	}
}
