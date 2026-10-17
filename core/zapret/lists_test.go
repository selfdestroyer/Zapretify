package zapret

import (
	"os"
	"testing"
)

func TestListManagerPathTraversal(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "zapret-lists-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	lm := NewListManager(tempDir)

	// Valid file test
	validFile := "user-general.txt"
	content := "domain.com\nanother.org\n"
	if err := lm.WriteRaw(validFile, content); err != nil {
		t.Fatalf("unexpected error writing valid file: %v", err)
	}

	readBack, err := lm.ReadRaw(validFile)
	if err != nil {
		t.Fatalf("unexpected error reading valid file: %v", err)
	}
	if readBack != content {
		t.Errorf("expected %q, got %q", content, readBack)
	}

	// Traversal attacks
	attacks := []string{
		"../secret.txt",
		"..\\secret.txt",
		"/etc/passwd",
		"C:\\Windows\\System32\\calc.exe",
		"sub/folder.txt",
		"sub\\folder.txt",
		"....//....//file.txt",
		"../",
		"..",
	}

	for _, attack := range attacks {
		t.Run("Attack_"+attack, func(t *testing.T) {
			_, err := lm.ReadRaw(attack)
			if err == nil {
				t.Errorf("expected error for path traversal attack %q, got nil", attack)
			}

			err = lm.WriteRaw(attack, "malicious content")
			if err == nil {
				t.Errorf("expected error for path traversal write attack %q, got nil", attack)
			}
		})
	}
}
