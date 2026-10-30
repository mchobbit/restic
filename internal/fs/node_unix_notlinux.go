//go:build !linux && unix

package fs

import (
	"syscall"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// utimesNano is like syscall.UtimesNano, except that it does not follow
// symlinks: a symlink gets its own times back through utimensat(2) with
// AT_SYMLINK_NOFOLLOW (FreeBSD, illumos, OpenBSD, NetBSD, macOS), as on
// Linux, instead of keeping the time of the restore.  Where the system can't
// do that, the symlink's times are left as they are.
func utimesNano(path string, atime, mtime int64, typ data.NodeType) error {
	if typ != data.NodeTypeSymlink {
		return syscall.UtimesNano(path, []syscall.Timespec{
			syscall.NsecToTimespec(atime),
			syscall.NsecToTimespec(mtime),
		})
	}
	times := []unix.Timespec{unix.NsecToTimespec(atime), unix.NsecToTimespec(mtime)}
	err := unix.UtimesNanoAt(unix.AT_FDCWD, path, times, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
		return nil
	}
	return err
}
