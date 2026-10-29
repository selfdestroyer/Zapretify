package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const launchAgentLabel = "io.github.selfdestroyer.zapretify"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

// appBundle is Zapretify.app that contains the running core (Contents/MacOS/zapretify-core).
func appBundle() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	app := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(app, ".app") {
		return "", errors.New("автозапуск доступен только из Zapretify.app")
	}
	return app, nil
}

// SetAppAutostart adds or removes a LaunchAgent that opens Zapretify at login, like the
// scheduled task on Windows: in the background and with --minimized.
func SetAppAutostart(enabled bool) error {
	path := launchAgentPath()
	if !enabled {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	app, err := appBundle()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array><string>/usr/bin/open</string><string>-g</string><string>-a</string><string>%s</string><string>--args</string><string>--minimized</string></array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`, launchAgentLabel, xmlEscape(app))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(plist), 0o644)
}

// IsAppAutostart reports whether the LaunchAgent exists and points at this Zapretify.app.
func IsAppAutostart() bool {
	data, err := os.ReadFile(launchAgentPath())
	if err != nil {
		return false
	}
	app, err := appBundle()
	return err == nil && strings.Contains(string(data), "<string>"+xmlEscape(app)+"</string>")
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
