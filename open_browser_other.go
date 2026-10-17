//go:build !windows

package main

import (
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

func openBrowser(rawURL string) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "tg" {
		return
	}

	cleanURL := parsed.String()
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", cleanURL).Start()
	default:
		_ = exec.Command("xdg-open", cleanURL).Start()
	}
}
