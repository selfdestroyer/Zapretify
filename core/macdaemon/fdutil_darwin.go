package macdaemon

import "golang.org/x/sys/unix"

func closeFD(fd int) { _ = unix.Close(fd) }

// unlinkAt removes a file (or a symlink itself, never its target) inside an open folder.
func unlinkAt(dir int, name string) error { return unix.Unlinkat(dir, name, 0) }
