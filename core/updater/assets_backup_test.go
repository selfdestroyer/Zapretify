package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAssetsBackupRoundTrip(t *testing.T) {
	appDir := t.TempDir()
	listsDir := filepath.Join(appDir, "lists")
	writeTestFile(t, filepath.Join(appDir, "strategies.json"), `{"version":"1.10.3","strategies":[]}`)
	writeTestFile(t, filepath.Join(listsDir, "list-general.txt"), "old.example\n")
	writeTestFile(t, filepath.Join(listsDir, "list-general-user.txt"), "mine.example\n")
	writeTestFile(t, filepath.Join(appDir, "bin", "quic.bin"), "old-quic")
	writeTestFile(t, filepath.Join(appDir, "bin", "winws.exe"), "exe")

	if info := ReadAssetsBackup(appDir); info.Exists {
		t.Fatal("backup exists before any update")
	}

	// A synchronisation replaces the bases and keeps the snapshot.
	staging, err := PrepareAssetsBackup(listsDir, appDir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(appDir, "strategies.json"), `{"version":"1.10.4","strategies":[]}`)
	writeTestFile(t, filepath.Join(listsDir, "list-general.txt"), "new.example\n")
	writeTestFile(t, filepath.Join(appDir, "bin", "quic.bin"), "new-quic")
	writeTestFile(t, filepath.Join(appDir, "bin", "added.bin"), "added")
	if err := CommitAssetsBackup(appDir, staging); err != nil {
		t.Fatal(err)
	}

	info := ReadAssetsBackup(appDir)
	if !info.Exists || info.Version != "1.10.3" {
		t.Fatalf("backup info = %+v", info)
	}
	for _, f := range info.Files {
		if f == "lists/list-general-user.txt" || f == "bin/winws.exe" {
			t.Errorf("snapshot contains %s", f)
		}
	}

	// A synchronisation that changed nothing must not replace the snapshot.
	staging, err = PrepareAssetsBackup(listsDir, appDir)
	if err != nil {
		t.Fatal(err)
	}
	DiscardAssetsBackup(staging)
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Error("staging folder left behind")
	}
	if v := ReadAssetsBackup(appDir).Version; v != "1.10.3" {
		t.Fatalf("snapshot replaced by a no-op run: %s", v)
	}

	writeTestFile(t, filepath.Join(listsDir, "list-general-user.txt"), "mine2.example\n")
	restored, err := RestoreAssetsBackup(listsDir, appDir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != "1.10.3" {
		t.Errorf("restored version = %s", restored.Version)
	}
	if got := readTestFile(t, filepath.Join(listsDir, "list-general.txt")); got != "old.example\n" {
		t.Errorf("list-general.txt = %q", got)
	}
	if got := readTestFile(t, filepath.Join(appDir, "bin", "quic.bin")); got != "old-quic" {
		t.Errorf("quic.bin = %q", got)
	}
	if got := readTestFile(t, filepath.Join(listsDir, "list-general-user.txt")); got != "mine2.example\n" {
		t.Errorf("user list changed by rollback: %q", got)
	}
	if _, err := os.Stat(filepath.Join(appDir, "bin", "added.bin")); err != nil {
		t.Error("a fake added by the update was removed")
	}

	// The rollback itself can be undone: the replaced bases became the snapshot.
	if v := ReadAssetsBackup(appDir).Version; v != "1.10.4" {
		t.Fatalf("snapshot after rollback = %s", v)
	}
	if _, err := RestoreAssetsBackup(listsDir, appDir); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(listsDir, "list-general.txt")); got != "new.example\n" {
		t.Errorf("after undo list-general.txt = %q", got)
	}
}

func TestRestoreAssetsBackupRejectsBadPaths(t *testing.T) {
	appDir := t.TempDir()
	listsDir := filepath.Join(appDir, "lists")
	writeTestFile(t, filepath.Join(appDir, assetsBackupDir, assetsBackupMeta), `{"version":"x","files":["../evil.exe"]}`)
	writeTestFile(t, filepath.Join(appDir, "evil.exe"), "keep")
	if _, err := RestoreAssetsBackup(listsDir, appDir); err == nil {
		t.Fatal("restore accepted a path outside the bases")
	}
	if _, err := RestoreAssetsBackup(listsDir, t.TempDir()); err == nil {
		t.Fatal("restore without a backup succeeded")
	}
}
