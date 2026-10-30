//go:build linux

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeLinuxInodeFlags

// From <linux/fs.h>: the inode flags chattr(1) sets, s u c S i a d A m j t D T
// C P.  The rest belong to the file system (extents, inline data, ...) or
// need more than setting (DAX, casefolding, verity, encryption).
const linuxInodeFlags = 0x00000001 | // FS_SECRM_FL        s
	0x00000002 | // FS_UNRM_FL         u
	0x00000004 | // FS_COMPR_FL        c
	0x00000008 | // FS_SYNC_FL         S
	0x00000010 | // FS_IMMUTABLE_FL    i
	0x00000020 | // FS_APPEND_FL       a
	0x00000040 | // FS_NODUMP_FL       d
	0x00000080 | // FS_NOATIME_FL      A
	0x00000400 | // FS_NOCOMP_FL       m
	0x00004000 | // FS_JOURNAL_DATA_FL j
	0x00008000 | // FS_NOTAIL_FL       t
	0x00010000 | // FS_DIRSYNC_FL      D
	0x00020000 | // FS_TOPDIR_FL       T
	0x00800000 | // FS_NOCOW_FL        C
	0x20000000 //   FS_PROJINHERIT_FL  P

// openForFlags opens a regular file or directory for FS_IOC_GETFLAGS and
// FS_IOC_SETFLAGS, as lsattr(1) and chattr(1) do.  Other types of file have
// no flags, and opening a device or FIFO could have side effects.
func openForFlags(path string, typ data.NodeType) (int, error) {
	how := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	switch typ {
	case data.NodeTypeFile:
	case data.NodeTypeDir:
		how |= unix.O_DIRECTORY
	default:
		return -1, unix.ENOTSUP
	}
	fd, err := unix.Open(path, how, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return fd, nil
}

func nodeFillFileFlags(node *data.Node, path string, _ *ExtendedFileInfo) error {
	if node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir {
		return nil
	}
	fd, err := openForFlags(path, node.Type)
	if err != nil {
		// Not readable (not root), or gone: the backup reports that for
		// the contents, if it matters.
		debug.Log("no file flags for %v: %v", path, err)
		return nil
	}
	defer func() { _ = unix.Close(fd) }()
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		// A file system without inode flags (ENOTTY, EOPNOTSUPP, ...).
		debug.Log("no file flags for %v: %v", path, err)
		return nil
	}
	node.SetFileFlags(fileFlagsAttribute, flags&linuxInodeFlags)
	return nil
}

func restoreFileFlags(path string, typ data.NodeType, flags uint32) error {
	fd, err := openForFlags(path, typ)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	current, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		return &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
	}
	// Keep the file system's own flags as they are.
	own := current &^ linuxInodeFlags
	err = setFlagsBestEffort(flags&linuxInodeFlags, func(f uint32) error {
		return unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(own|f))
	})
	if err != nil {
		return &os.PathError{Op: "FS_IOC_SETFLAGS", Path: path, Err: err}
	}
	return nil
}
