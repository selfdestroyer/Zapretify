package zapret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPSetModes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "zapret_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Default or mode "none"
	if err := SetIPSetMode(tmpDir, "none"); err != nil {
		t.Fatalf("SetMode none failed: %v", err)
	}
	content, _ := os.ReadFile(filepath.Join(tmpDir, "ipset-all.txt"))
	if !strings.Contains(string(content), "203.0.113.113/32") {
		t.Errorf("expected dummy IP for none mode, got: %s", string(content))
	}
	if GetIPSetMode(tmpDir) != "none" {
		t.Errorf("expected mode none, got: %s", GetIPSetMode(tmpDir))
	}

	// Mode "any"
	if err := SetIPSetMode(tmpDir, "any"); err != nil {
		t.Fatalf("SetMode any failed: %v", err)
	}
	content, _ = os.ReadFile(filepath.Join(tmpDir, "ipset-all.txt"))
	if len(content) != 0 {
		t.Errorf("expected empty file for any mode, got %d bytes", len(content))
	}
	if GetIPSetMode(tmpDir) != "any" {
		t.Errorf("expected mode any, got: %s", GetIPSetMode(tmpDir))
	}
}
