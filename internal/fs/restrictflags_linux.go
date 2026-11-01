package fs

import (
	"errors"
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// ClearRestrictingFlags clears immutable and append-only (i, a) on the
// regular file or directory at path, which would keep a restore from
// rewriting, replacing or changing it, before the restorer touches a file
// it finds in its way, leaving the other flags as they are; the late flags
// pass gives the file its node's.  Through FS_IOC_FSSETXATTR, which leaves
// btrfs's btrfs.compression property alone (FS_IOC_SETFLAGS rewrites it),
// or FS_IOC_SETFLAGS where the file system has no FS_IOC_FSSETXATTR.
// Symlinks and special files have no flags; nothing there, or no such
// flags, is nothing to do.  A failure (not root) is only logged: what the
// restorer does next to the file fails and says why.  As in rustic-bko.
func ClearRestrictingFlags(path string) {
	if err := clearRestrictingFlags(path); err != nil {
		debug.Log("restricting flags of %v not cleared: %v", path, err)
	}
}

func clearRestrictingFlags(path string) error {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	var typ data.NodeType
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		typ = data.NodeTypeFile
	case unix.S_IFDIR:
		typ = data.NodeTypeDir
	default:
		return nil
	}
	fd, err := openForFlags(path, typ, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	current, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		return &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
	}
	if current&fsImmutableAppend == 0 {
		return nil
	}
	var fsx fsxattr
	err = ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx)
	if err == nil {
		fsx.xflags &^= fsXflagImmutable | fsXflagAppend
		err = ioctlFsxattr(fd, fsIOCFSSetXattr, &fsx)
	}
	if err == nil {
		return nil
	}
	if !unsupportedErr(err) {
		return &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}
	}
	if err := unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(current&^fsImmutableAppend)); err != nil {
		return &os.PathError{Op: "FS_IOC_SETFLAGS", Path: path, Err: err}
	}
	return nil
}
