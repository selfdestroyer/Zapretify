package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateInstallPath(t *testing.T) {
	dir := t.TempDir()
	ok, err := validateInstallPath(filepath.Join(dir, "My App", "Zapretify"))
	if err != nil || ok == "" {
		t.Fatalf("valid path rejected: %v", err)
	}

	bad := []string{"", "   ", "relative/path", ".", string(filepath.Separator), filepath.Join(dir, "a\"b"), filepath.Join(dir, "a|b"), filepath.Join(dir, "x\nz")}
	for _, p := range bad {
		if _, err := validateInstallPath(p); err == nil {
			t.Errorf("path %q must be rejected", p)
		}
	}

	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateInstallPath(file); err == nil {
		t.Error("existing file must be rejected")
	}

	t.Setenv("SystemRoot", filepath.Join(dir, "Windows"))
	if _, err := validateInstallPath(filepath.Join(dir, "Windows", "System32", "Z")); err == nil {
		t.Error("path inside SystemRoot must be rejected")
	}
}

func TestIsWithin(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "a", "b")
	if !isWithin(root, filepath.Join(root, "c", "d")) || !isWithin(root, root) {
		t.Error("child/self must be within")
	}
	if isWithin(root, filepath.Join(string(filepath.Separator), "a", "bb")) || isWithin(root, filepath.Join(root, "..", "x")) {
		t.Error("sibling/parent must not be within")
	}
}
