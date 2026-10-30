//go:build linux

package fs

import (
	"os"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// Linux metadata that restic otherwise misses, besides the inode flags
// (flags_linux.go): a file's project ID, which project quotas on XFS, ext4
// and f2fs count its blocks against (chattr -p, xfs_quota project -s).
// Without it a restored tree is counted against project 0, and a directory
// with FS_PROJINHERIT_FL hands project 0 to the files created in it.

// fsxattr is struct fsxattr from <linux/fs.h>, 28 bytes.
type fsxattr struct {
	xflags     uint32
	extsize    uint32
	nextents   uint32
	projid     uint32
	cowextsize uint32
	pad        [8]byte
}

// FS_IOC_FSGETXATTR and FS_IOC_FSSETXATTR, _IOR('X', 31, struct fsxattr) and
// _IOW('X', 32, struct fsxattr), which x/sys/unix lacks: 0x801c581f and
// 0x401c5820 on x86, ARM and RISC-V.  The direction bits are those of
// FS_IOC_GETFLAGS and FS_IOC_SETFLAGS, as they differ on MIPS, PowerPC and
// SPARC.
const (
	fsIOCFSGetXattr uint = unix.FS_IOC_GETFLAGS&0xc0000000 | 28<<16 | 'X'<<8 | 31
	fsIOCFSSetXattr uint = unix.FS_IOC_SETFLAGS&0xc0000000 | 28<<16 | 'X'<<8 | 32
)

func ioctlFsxattr(fd int, req uint, fsx *fsxattr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(unsafe.Pointer(fsx)))
	if errno != 0 {
		return errno
	}
	return nil
}

// nodeFillProjectID records the project ID of the regular file or directory
// open as fd, when it isn't 0.
func nodeFillProjectID(node *data.Node, path string, fd int) {
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		// A file system without project IDs (ENOTTY, EOPNOTSUPP, EINVAL).
		debug.Log("no project ID for %v: %v", path, err)
		return
	}
	node.SetProjectID(fsx.projid)
}

// nodeFillOSAttributes has nothing to add on Linux: nodeFillFileFlags reads
// the project ID along with the flags.
func nodeFillOSAttributes(_ *data.Node, _ string, _ *ExtendedFileInfo) error {
	return nil
}

// nodeRestoreOSAttributes restores the project ID, with the extended
// attributes, before the file flags: an immutable file refuses a new one.
func nodeRestoreOSAttributes(node *data.Node, path string) error {
	id, ok, err := node.ProjectID()
	if !ok || err != nil {
		return err
	}
	fd, err := openForFlags(path, node.Type)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		return &os.PathError{Op: "FS_IOC_FSGETXATTR", Path: path, Err: err}
	}
	if fsx.projid == id {
		return nil
	}
	fsx.projid = id
	if err := ioctlFsxattr(fd, fsIOCFSSetXattr, &fsx); err != nil {
		return &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}
	}
	return nil
}

func nodeRestoreACLs(_ *data.Node, _ string) error {
	return nil
}

// A symlink's own permissions don't matter on Linux, which has none.
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
