//go:build !windows

package macdaemon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/unix"
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

// ImportBases copies strategies.json, lists/*.txt and bin/*.bin from home/rel into rootDir.
//
// The user's files are untrusted input for a root process, and the copies end up readable by
// everyone, so root must never be tricked into reading a file the user could not read:
//   - home is trusted (its parent belongs to root), everything below it is opened component by
//     component with O_NOFOLLOW: no symlinked folder or file is followed;
//   - every file must belong to uid (this also refuses hard links to other users' or root's files),
//     be a regular file and fit the size limit; odd names are skipped.
//
// lists/ and bin/ in rootDir are replaced as a whole, so files deleted by the user disappear there too.
func ImportBases(home, rel, rootDir string, uid int) (Bases, error) {
	dir, err := openBelow(home, rel)
	if err != nil {
		return Bases{}, fmt.Errorf("папка данных Zapretify: %w", err)
	}
	defer unix.Close(dir)

	strategies, err := readOwned(dir, "strategies.json", uid, maxStrategiesSize)
	if err != nil {
		return Bases{}, err
	}
	b := Bases{Strategies: strategies, ListsDir: filepath.Join(rootDir, "lists"), BinDir: filepath.Join(rootDir, "bin")}
	if err := copyDir(dir, "lists", b.ListsDir, uid, listName, maxListSize); err != nil {
		return Bases{}, err
	}
	if err := copyDir(dir, "bin", b.BinDir, uid, binName, maxBinSize); err != nil {
		return Bases{}, err
	}
	return b, nil
}

// openBelow opens home and then each component of rel with O_NOFOLLOW|O_DIRECTORY.
func openBelow(home, rel string) (int, error) {
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("%s: %w", home, err)
	}
	for _, part := range strings.Split(filepath.Clean(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			unix.Close(fd)
			return -1, fmt.Errorf("недопустимый путь %q", rel)
		}
		next, err := openatDir(fd, part)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

func openatDir(dir int, name string) (int, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		// ELOOP / ENOTDIR: a symlink or a file where a folder is expected.
		return -1, fmt.Errorf("%s не является папкой: %w", name, err)
	}
	return fd, nil
}

// readOwned reads dir/name if it is a regular file of uid no larger than limit.
func readOwned(dir int, name string, uid int, limit int64) ([]byte, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("чтение %s: %w", name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, fmt.Errorf("%s не является обычным файлом", name)
	}
	if int(st.Uid) != uid {
		return nil, fmt.Errorf("%s принадлежит другому пользователю (uid %d)", name, st.Uid)
	}
	if st.Size > limit {
		return nil, fmt.Errorf("%s слишком большой (%d байт)", name, st.Size)
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// copyDir replaces dst with the files of parent/name whose names match pattern.
func copyDir(parent int, name, dst string, uid int, pattern *regexp.Regexp, limit int64) error {
	dir, err := openatDir(parent, name)
	if err != nil {
		return err
	}
	// os.File takes over dir; reading the listing does not disturb openat on the same fd.
	df := os.NewFile(uintptr(dir), name)
	defer df.Close()
	names, err := df.Readdirnames(-1)
	if err != nil {
		return err
	}
	staging := dst + ".new"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0755); err != nil {
		return err
	}
	for _, n := range names {
		if !pattern.MatchString(n) {
			continue
		}
		data, err := readOwned(dir, n, uid, limit)
		if err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, n), data, 0644); err != nil {
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
