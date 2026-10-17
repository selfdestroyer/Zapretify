package main

import (
	"net/url"
	"testing"
)

func TestValidators(t *testing.T) {
	// Method validator
	if !validBenchmarkMethod("") || !validBenchmarkMethod("all") || !validBenchmarkMethod("dpi") || !validBenchmarkMethod("https") {
		t.Errorf("expected valid methods to pass")
	}
	if validBenchmarkMethod("curl; rm -rf /") || validBenchmarkMethod("unknown") {
		t.Errorf("expected malicious/unknown methods to fail")
	}

	// Target group validator
	if !validBenchmarkTargetGroup("") || !validBenchmarkTargetGroup("all") || !validBenchmarkTargetGroup("yt_discord") || !validBenchmarkTargetGroup("blockcheck") {
		t.Errorf("expected valid target groups to pass")
	}
	if validBenchmarkTargetGroup("malicious_group") {
		t.Errorf("expected unknown target group to fail")
	}

	// IPSet mode validator
	if !validIPSetMode("none") || !validIPSetMode("loaded") || !validIPSetMode("any") {
		t.Errorf("expected valid ipset modes to pass")
	}
	if validIPSetMode("custom") || validIPSetMode("inject") {
		t.Errorf("expected invalid ipset modes to fail")
	}

	// Allowed URL validator
	allowedURLs := []string{
		"https://github.com/selfdestroyer/Zapretify",
		"https://api.github.com/repos/selfdestroyer/Zapretify/releases/latest",
		"https://raw.githubusercontent.com/selfdestroyer/Zapretify/main/version.json",
		"https://t.me/zapretify",
		"tg://proxy?server=127.0.0.1&port=1443&secret=123",
	}
	for _, uStr := range allowedURLs {
		parsed, err := url.Parse(uStr)
		if err != nil || !isAllowedURL(parsed) {
			t.Errorf("expected %q to be allowed, got err=%v", uStr, err)
		}
	}

	blockedURLs := []string{
		"file:///C:/Windows/System32/cmd.exe",
		"cmd.exe",
		"powershell://invoke",
		"https://evil.com/payload.exe",
		"http://localhost:8080/exploit",
		"javascript:alert(1)",
		"data:text/html,<h1>test</h1>",
	}
	for _, uStr := range blockedURLs {
		parsed, err := url.Parse(uStr)
		if err == nil && isAllowedURL(parsed) {
			t.Errorf("expected %q to be blocked", uStr)
		}
	}
}
