//go:build freebsd || netbsd

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// nodeRestoreBirthTime sets node's birth time, when it is earlier than its
// mtime, as the file's mtime, through utimensat(2) with the atime omitted:
// the kernel lowers the birth time with it, and the final times, set next,
// leave it (birthtime.go).  A symlink is not followed; where that can't be
// done, its times are left as they are, as utimesNano leaves them.
func nodeRestoreBirthTime(node *data.Node, path string) error {
	birth, ok, err := birthTimeFirst(node)
	if !ok || err != nil {
		return err
	}
	times := []unix.Timespec{{Nsec: utimeOmit}, unix.NsecToTimespec(birth.UnixNano())}
	err = unix.UtimesNanoAt(unix.AT_FDCWD, path, times, unix.AT_SYMLINK_NOFOLLOW)
	if node.Type == data.NodeTypeSymlink &&
		(errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL)) {
		return nil
	}
	if err != nil {
		return &os.PathError{Op: "utimensat (birth time)", Path: path, Err: err}
	}
	return nil
}
