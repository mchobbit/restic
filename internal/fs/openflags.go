//go:build linux || illumos || freebsd

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"golang.org/x/sys/unix"
)

// openForFlags opens a regular file or directory for FS_IOC_GETFLAGS and
// FS_IOC_SETFLAGS, as lsattr(1) and chattr(1) do (on illumos for ZFS's
// project ioctls, as zfs project does, and on FreeBSD for them too).
// Other types of file have no flags, and opening a device or FIFO could
// have side effects.  The file opened
// must be of type typ, and, when fi is given (at backup), the inode fi
// describes.
func openForFlags(path string, typ data.NodeType, fi *ExtendedFileInfo) (int, error) {
	how := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY | unix.O_CLOEXEC
	var mode uint32
	switch typ {
	case data.NodeTypeFile:
		mode = unix.S_IFREG
	case data.NodeTypeDir:
		how |= unix.O_DIRECTORY
		mode = unix.S_IFDIR
	default:
		return -1, unix.ENOTSUP
	}
	fd, err := unix.Open(path, how, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if uint32(st.Mode)&unix.S_IFMT != mode || fi != nil && (uint64(st.Dev) != fi.DeviceID || st.Ino != fi.Inode) {
		_ = unix.Close(fd)
		return -1, &os.PathError{Op: "open", Path: path, Err: errNotNodeType}
	}
	return fd, nil
}
