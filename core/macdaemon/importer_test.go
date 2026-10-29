//go:build !windows

package macdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRel = "Library/Application Support/Zapretify"

func writeFile(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// testHome is a fake home folder with real paths (t.TempDir sits under the /var symlink on macOS).
func testHome(t *testing.T) (home, data string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data = filepath.Join(home, testRel)
	writeFile(t, filepath.Join(data, "strategies.json"), `{"strategies":[]}`)
	writeFile(t, filepath.Join(data, "lists", "list-general.txt"), "discord.com\n")
	writeFile(t, filepath.Join(data, "bin", "quic.bin"), "\x01\x02")
	return home, data
}

func TestImportBases(t *testing.T) {
	home, data := testHome(t)
	root := t.TempDir()
	uid := os.Getuid()
	writeFile(t, filepath.Join(data, "lists", "ipset-all.txt.backup"), "1.2.3.0/24\n") // not *.txt: skipped
	writeFile(t, filepath.Join(data, "bin", "winws.exe"), "MZ")                        // not a fake: skipped

	b, err := ImportBases(home, testRel, root, uid)
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Strategies) != `{"strategies":[]}` {
		t.Fatalf("strategies = %q", b.Strategies)
	}
	assertFiles(t, b.ListsDir, "list-general.txt")
	assertFiles(t, b.BinDir, "quic.bin")

	// A list removed by the user disappears from the daemon's copy on the next import.
	_ = os.Remove(filepath.Join(data, "lists", "list-general.txt"))
	writeFile(t, filepath.Join(data, "lists", "list-google.txt"), "youtube.com\n")
	if _, err := ImportBases(home, testRel, root, uid); err != nil {
		t.Fatal(err)
	}
	assertFiles(t, b.ListsDir, "list-google.txt")
}

func TestImportBasesRefusesSymlinks(t *testing.T) {
	uid := os.Getuid()
	secret := filepath.Join(t.TempDir(), "secret")
	writeFile(t, secret, "root only\n")

	// A symlinked list file.
	home, data := testHome(t)
	if err := os.Symlink(secret, filepath.Join(data, "lists", "list-exclude.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBases(home, testRel, t.TempDir(), uid); err == nil {
		t.Fatal("a symlinked list must be refused")
	}

	// lists/ itself as a symlink.
	home, data = testHome(t)
	_ = os.RemoveAll(filepath.Join(data, "lists"))
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(data, "lists")); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBases(home, testRel, t.TempDir(), uid); err == nil || !strings.Contains(err.Error(), "не является папкой") {
		t.Fatalf("a symlinked lists dir must be refused, got %v", err)
	}

	// A folder above the data dir as a symlink ("Application Support" → somewhere else).
	home, data = testHome(t)
	elsewhere := filepath.Join(t.TempDir(), "AS")
	if err := os.Rename(filepath.Dir(data), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Dir(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportBases(home, testRel, t.TempDir(), uid); err == nil {
		t.Fatal("a symlinked parent folder must be refused")
	}

	if _, err := ImportBases(home, "Library/../../etc", t.TempDir(), uid); err == nil {
		t.Fatal(".. must be refused")
	}
}

// Files of another user (or hard links to them) are refused: the copies are readable by everyone.
func TestImportBasesRefusesForeignFiles(t *testing.T) {
	home, _ := testHome(t)
	if _, err := ImportBases(home, testRel, t.TempDir(), os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "другому пользователю") {
		t.Fatalf("files of another uid must be refused, got %v", err)
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
