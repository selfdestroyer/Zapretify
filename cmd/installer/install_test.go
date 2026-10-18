package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func buildZip(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestExtractPayloadProtectsUserData(t *testing.T) {
	dir := t.TempDir()
	zr := buildZip(t, map[string]string{
		"Zapretify.exe":               "NEW",
		"config.json":                 `{"telegram":{"secret":"shipped"}}`,
		"lists/list-general.txt":      "upstream",
		"lists/list-general-user.txt": "template",
		"lists/ipset-all-user.txt":    "template",
		"custom.txt":                  "template",
		"README.md":                   "docs",
		"../evil.txt":                 "zip-slip",
		"/abs.txt":                    "abs",
		`C:\drive.txt`:                "drive",
	})

	// pre-existing user data
	user := filepath.Join(dir, "lists", "list-general-user.txt")
	cfg := filepath.Join(dir, "config.json")
	_ = os.MkdirAll(filepath.Dir(user), 0755)
	_ = os.WriteFile(user, []byte("MY DOMAINS"), 0644)
	_ = os.WriteFile(cfg, []byte("MY CONFIG"), 0644)

	if failed := extractPayload(zr, dir, nil); len(failed) != 0 {
		t.Fatalf("unexpected failures: %v", failed)
	}

	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dir, p)); return string(b) }
	if read("Zapretify.exe") != "NEW" || read("lists/list-general.txt") != "upstream" {
		t.Error("regular files must be extracted")
	}
	if read("lists/list-general-user.txt") != "MY DOMAINS" {
		t.Error("existing *-user.txt must be preserved")
	}
	if read("lists/ipset-all-user.txt") != "template" || read("custom.txt") != "template" {
		t.Error("missing user-owned files must be created from the template")
	}
	if read("config.json") != "MY CONFIG" {
		t.Error("existing config.json must not be touched")
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("docs must not be installed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); err == nil {
		t.Error("zip-slip entry escaped the target dir")
	}
	for _, n := range []string{"abs.txt", "drive.txt"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s must be skipped", n)
		}
	}
}

func TestExtractPayloadFreshInstallHasNoConfig(t *testing.T) {
	dir := t.TempDir()
	zr := buildZip(t, map[string]string{"config.json": "shipped", "Zapretify.exe": "x"})
	if failed := extractPayload(zr, dir, nil); len(failed) != 0 {
		t.Fatalf("failures: %v", failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("config.json from the payload must never be installed")
	}
}

func TestExtractPayloadReportsFailures(t *testing.T) {
	dir := t.TempDir()
	// a directory where a file must be written -> rename fails
	if err := os.MkdirAll(filepath.Join(dir, "Zapretify.exe", "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	zr := buildZip(t, map[string]string{"Zapretify.exe": "x"})
	if failed := extractPayload(zr, dir, nil); len(failed) != 1 {
		t.Fatalf("expected 1 failure, got %v", failed)
	}
}

func TestBeginInstallRejectsBadPathAndConcurrency(t *testing.T) {
	if err := beginInstall(InstallRequest{Path: "relative"}); err == nil {
		t.Fatal("relative path must be rejected")
	}
	progressMu.Lock()
	installRunning = true
	progressMu.Unlock()
	defer func() { progressMu.Lock(); installRunning = false; progressMu.Unlock() }()
	if err := beginInstall(InstallRequest{Path: t.TempDir()}); err == nil {
		t.Fatal("second concurrent install must be rejected")
	}
}
