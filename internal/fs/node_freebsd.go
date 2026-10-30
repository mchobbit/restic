//go:build freebsd

package fs

import (
	"os"
	"syscall"

	"github.com/restic/restic/internal/data"
	"golang.org/x/sys/unix"
)

func nodeRestoreSymlinkTimestamps(path string, utimes [2]syscall.Timespec) error {
	return nil
}

// utimesNano is like syscall.UtimesNano, except that it does not follow
// symlinks: FreeBSD has utimensat(2), so a symlink gets its own timestamps
// back, as on Linux, instead of keeping the time of the restore.
func utimesNano(path string, atime, mtime int64, _ data.NodeType) error {
	times := []unix.Timespec{
		unix.NsecToTimespec(atime),
		unix.NsecToTimespec(mtime),
	}
	return unix.UtimesNanoAt(unix.AT_FDCWD, path, times, unix.AT_SYMLINK_NOFOLLOW)
}

func mknod(path string, mode uint32, dev uint64) error {
	err := syscall.Mknod(path, mode, dev)
	if err != nil {
		err = &os.PathError{Op: "mknod", Path: path, Err: err}
	}
	return err
}
