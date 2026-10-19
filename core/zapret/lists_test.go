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

	// Valid file tests
	validFiles := []string{"list-general.txt", "custom-rules-user.txt", "ipset-all.txt"}
	for _, vf := range validFiles {
		content := "domain.com\nanother.org\n"
		if err := lm.WriteRaw(vf, content); err != nil {
			t.Fatalf("unexpected error writing valid file %s: %v", vf, err)
		}

		readBack, err := lm.ReadRaw(vf)
		if err != nil {
			t.Fatalf("unexpected error reading valid file %s: %v", vf, err)
		}
		if readBack != content {
			t.Errorf("expected %q, got %q", content, readBack)
		}
	}

	// 5MB limit test
	hugeContent := string(make([]byte, 6*1024*1024))
	if err := lm.WriteRaw("list-general.txt", hugeContent); err == nil {
		t.Errorf("expected error when writing >5MB content, got nil")
	}

	// Traversal attacks, extensions, NTFS ADS, DOS device names, and non-whitelisted names
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
		"exploit.exe",
		"malware.bat",
		"payload.ps1",
		"file.txt:stream",
		"test.txt:$DATA",
		"CON.txt",
		"con.txt",
		"CON.foo.txt",
		"con.bar.user.txt",
		"NUL.txt",
		"aux.txt",
		"prn.txt",
		"com1.txt",
		"lpt1.txt",
		"unknown-list.txt",
		"hacked.txt",
	}

	for _, attack := range attacks {
		t.Run("Attack_"+attack, func(t *testing.T) {
			_, err := lm.ReadRaw(attack)
			if err == nil {
				t.Errorf("expected error for attack %q, got nil", attack)
			}

			err = lm.WriteRaw(attack, "malicious content")
			if err == nil {
				t.Errorf("expected error for write attack %q, got nil", attack)
			}
		})
	}
}

func TestEnsureNotEmptyUserList(t *testing.T) {
	if got := EnsureNotEmptyUserList("list-general-user.txt", "# только комментарий\n\n"); got != "# только комментарий\ndomain.example.abc\n" {
		t.Fatalf("пустой список должен получить заглушку, получено %q", got)
	}
	if got := EnsureNotEmptyUserList("list-general-user.txt", "example.org\n"); got != "example.org\n" {
		t.Fatalf("непустой список не должен меняться, получено %q", got)
	}
	if got := EnsureNotEmptyUserList("ipset-exclude-user.txt", ""); got != "203.0.113.113/32\n" {
		t.Fatalf("пустой ipset должен получить заглушку, получено %q", got)
	}
	if got := EnsureNotEmptyUserList("list-general.txt", ""); got != "" {
		t.Fatalf("файлы из базы не трогаем, получено %q", got)
	}
}
