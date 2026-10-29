//go:build !windows

package macdaemon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

const (
	maxStrategiesSize = 5 << 20
	maxListSize       = 32 << 20 // ipset-all.txt is about 0.6 MB now
	maxBinSize        = 1 << 20
)

var (
	listName = regexp.MustCompile(`^[A-Za-z0-9_-]+\.txt$`)
	binName  = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.bin$`)
)

// Bases are the user's strategies and the copies of lists and fakes the engine will use.
type Bases struct {
	Strategies []byte // strategies.json of the user
	ListsDir   string // daemon's copy of lists/
	BinDir     string // daemon's copy of bin/*.bin
}

// ImportBases copies strategies.json, lists/*.txt and bin/*.bin from the user's data dir into rootDir.
// The user's files are untrusted input for a root process: symlinks, non-regular files, odd names
// and oversized files are refused, and nothing outside srcDir is read. lists/ and bin/ in rootDir are
// replaced as a whole, so files deleted by the user disappear there too.
func ImportBases(srcDir, rootDir string) (Bases, error) {
	if err := realDir(srcDir); err != nil {
		return Bases{}, err
	}
	strategies, err := readRegular(filepath.Join(srcDir, "strategies.json"), maxStrategiesSize)
	if err != nil {
		return Bases{}, err
	}
	b := Bases{Strategies: strategies, ListsDir: filepath.Join(rootDir, "lists"), BinDir: filepath.Join(rootDir, "bin")}
	if err := copyDir(filepath.Join(srcDir, "lists"), b.ListsDir, listName, maxListSize); err != nil {
		return Bases{}, err
	}
	if err := copyDir(filepath.Join(srcDir, "bin"), b.BinDir, binName, maxBinSize); err != nil {
		return Bases{}, err
	}
	return b, nil
}

func realDir(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("папка данных Zapretify: %w", err)
	}
	if !fi.IsDir() { // a symlink is not a dir for Lstat
		return fmt.Errorf("%s не является папкой", p)
	}
	return nil
}

func readRegular(p string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", filepath.Base(p), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s не является обычным файлом", filepath.Base(p))
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s слишком большой (%d байт)", filepath.Base(p), fi.Size())
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// copyDir replaces dst with the files of src whose names match pattern.
func copyDir(src, dst string, pattern *regexp.Regexp, limit int64) error {
	if err := realDir(src); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	staging := dst + ".new"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0755); err != nil {
		return err
	}
	for _, e := range entries {
		if !pattern.MatchString(e.Name()) {
			continue
		}
		data, err := readRegular(filepath.Join(src, e.Name()), limit)
		if err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, e.Name()), data, 0644); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
	}
	old := dst + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(dst, old); err != nil && !os.IsNotExist(err) {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	return os.RemoveAll(old)
}
