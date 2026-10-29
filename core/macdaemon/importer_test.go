//go:build !windows

package macdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestImportBases(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "strategies.json"), `{"strategies":[]}`)
	writeFile(t, filepath.Join(src, "lists", "list-general.txt"), "discord.com\n")
	writeFile(t, filepath.Join(src, "lists", "ipset-all.txt.backup"), "1.2.3.0/24\n") // not *.txt: skipped
	writeFile(t, filepath.Join(src, "bin", "quic.bin"), "\x01\x02")
	writeFile(t, filepath.Join(src, "bin", "winws.exe"), "MZ") // not a fake: skipped

	b, err := ImportBases(src, root)
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Strategies) != `{"strategies":[]}` {
		t.Fatalf("strategies = %q", b.Strategies)
	}
	assertFiles(t, b.ListsDir, "list-general.txt")
	assertFiles(t, b.BinDir, "quic.bin")

	// A list removed by the user disappears from the daemon's copy on the next import.
	_ = os.Remove(filepath.Join(src, "lists", "list-general.txt"))
	writeFile(t, filepath.Join(src, "lists", "list-google.txt"), "youtube.com\n")
	if _, err := ImportBases(src, root); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, b.ListsDir, "list-google.txt")
}

func TestImportBasesRefusesSymlinks(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "strategies.json"), `{}`)
	writeFile(t, filepath.Join(src, "bin", "quic.bin"), "x")
	writeFile(t, filepath.Join(src, "lists", "list-general.txt"), "a\n")

	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, "root only\n")
	if err := os.Symlink(secret, filepath.Join(src, "lists", "list-exclude.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBases(src, root); err == nil {
		t.Fatal("a symlinked list must be refused")
	}

	// The whole lists/ dir as a symlink is refused too.
	src2 := t.TempDir()
	writeFile(t, filepath.Join(src2, "strategies.json"), `{}`)
	writeFile(t, filepath.Join(src2, "bin", "quic.bin"), "x")
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(src2, "lists")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBases(src2, root); err == nil || !strings.Contains(err.Error(), "не является папкой") {
		t.Fatalf("a symlinked lists dir must be refused, got %v", err)
	}
}

func assertFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: got %v, want %v", dir, got, want)
	}
}
