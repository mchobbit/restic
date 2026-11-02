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
	return clearRestrictingWith(path, restrictingFlagOps{
		get: func() (uint32, error) { return unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS) },
		set: func(f uint32) error { return unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(f)) },
		clearXflags: func(xflags uint32) error {
			var fsx fsxattr
			if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
				return err
			}
			fsx.xflags &^= xflags
			return ioctlFsxattr(fd, fsIOCFSSetXattr, &fsx)
		},
	})
}

// restrictingFlagOps are the calls clearRestrictingWith makes on a file.
type restrictingFlagOps struct {
	get         func() (uint32, error) // FS_IOC_GETFLAGS
	set         func(uint32) error     // FS_IOC_SETFLAGS
	clearXflags func(uint32) error     // FS_IOC_FSGETXATTR, then FS_IOC_FSSETXATTR without these fsx_xflags
}

// clearRestrictingWith clears immutable and append-only of the file at
// path, through ops: by its fsx_xflags, then by its flags where the file
// system has no FS_IOC_FSSETXATTR, or where that left either set.  On
// Ubuntu 24.04's OpenZFS (2.2) FS_IOC_FSSETXATTR cleared immutable alone
// and left append-only, as if it took fsx_xflags for FS_IOC_GETFLAGS's
// flags, in which FS_XFLAG_APPEND is FS_IMMUTABLE_FL; a restore over an
// append-only file failed there (ubuntu24, round 15).  OpenZFS 2.3.9 clears
// both.  As in rustic-bko.
func clearRestrictingWith(path string, ops restrictingFlagOps) error {
	current, err := ops.get()
	if err != nil {
		return &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
	}
	if current&fsImmutableAppend == 0 {
		return nil
	}
	err = ops.clearXflags(fsXflagImmutable | fsXflagAppend)
	switch {
	case err == nil:
		current, err = ops.get()
		if err != nil {
			return &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
		}
		if current&fsImmutableAppend == 0 {
			return nil
		}
	case !unsupportedErr(err):
		return &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}
	}
	if err := ops.set(current &^ fsImmutableAppend); err != nil {
		return &os.PathError{Op: "FS_IOC_SETFLAGS", Path: path, Err: err}
	}
	return nil
}
