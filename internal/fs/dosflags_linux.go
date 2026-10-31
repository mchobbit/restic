//go:build linux

package fs

import (
	"os"
	"sync"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// OpenZFS keeps a file's DOS flags (readonly, hidden, system, archive, ...),
// which FreeBSD shows in st_flags and illumos as system attributes, on Linux
// too, where only its own ioctls on the file reach them.  Immutable,
// append-only and nodump are among them, but FS_IOC_GETFLAGS shows those as
// i, a and d, so linux.inode_flags has them already.

const zfsDosFlagsAttribute = data.TypeLinuxZFSDosFlags

// ZFS_IOC_GETDOSFLAGS and ZFS_IOC_SETDOSFLAGS, _IOR(0x83, 1, uint64_t) and
// _IOW(0x83, 2, uint64_t) from OpenZFS's <sys/fs/zfs.h>: 0x80088301 and
// 0x40088302 on x86, ARM and RISC-V, with the direction bits of
// FS_IOC_GETFLAGS and FS_IOC_SETFLAGS, as for fsIOCFSGetXattr.  Another
// file system may give these numbers another meaning, so they are only
// issued on ZFS (isZFS).
const (
	zfsIOCGetDosFlags uint = unix.FS_IOC_GETFLAGS&0xc0000000 | 8<<16 | 0x83<<8 | 1
	zfsIOCSetDosFlags uint = unix.FS_IOC_SETFLAGS&0xc0000000 | 8<<16 | 0x83<<8 | 2
)

// zfsSuperMagic is ZFS_SUPER_MAGIC, statfs(2)'s f_type on ZFS.
const zfsSuperMagic = 0x2fc12fc1

// The ZFS DOS flags kept, and the ones every new file has: ZFS sets archive
// on every file it creates, and again on every write to it.  Immutable,
// append-only and nodump are inode flags (linux.inode_flags); reparse can
// be read but never set on Linux; the rest of the ioctl's flags (opaque,
// av_quarantined, av_modified) it can't set at all.
const (
	zfsReadonly   = 0x0000000100000000 // ZFS_READONLY
	zfsHidden     = 0x0000000200000000 // ZFS_HIDDEN
	zfsSystem     = 0x0000000400000000 // ZFS_SYSTEM
	zfsArchive    = 0x0000000800000000 // ZFS_ARCHIVE
	zfsNounlink   = 0x0000002000000000 // ZFS_NOUNLINK
	zfsOffline    = 0x0000100000000000 // ZFS_OFFLINE
	zfsSparse     = 0x0000200000000000 // ZFS_SPARSE
	zfsDosFlags   = zfsReadonly | zfsHidden | zfsSystem | zfsArchive | zfsNounlink | zfsOffline | zfsSparse
	zfsDosDefault = zfsArchive
)

// errNotZFS is the reason ZFS DOS flags aren't restored on a file system
// other than ZFS.
var errNotZFS = errors.New("not ZFS")

func ioctlDosFlags(fd int, req uint, flags *uint64) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(unsafe.Pointer(flags)))
	if errno != 0 {
		return errno
	}
	return nil
}

// zfsDevices caches, by device (st_dev), whether a file system is ZFS, for
// the backup to ask fstatfs(2) once per file system.
var zfsDevices sync.Map

// isZFS tells whether the file open as fd, on device dev, is on ZFS.
func isZFS(fd int, dev uint64) bool {
	if zfs, ok := zfsDevices.Load(dev); ok {
		return zfs.(bool)
	}
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		debug.Log("fstatfs on device %#x: %v", dev, err)
		return false
	}
	zfs := uint32(st.Type) == zfsSuperMagic
	zfsDevices.Store(dev, zfs)
	return zfs
}

// keptZFSDosFlags returns those of dos, a file's ZFS DOS flags, that a node
// keeps, and whether it gets them: not when they are archive alone, as on
// every new or written file, so that a tree of ordinary files on ZFS gets no
// attribute; but when they are none at all, since a file restored without
// the attribute would get archive back from its contents' write.
func keptZFSDosFlags(dos uint64) (uint64, bool) {
	kept := dos & zfsDosFlags
	return kept, kept != zfsDosDefault
}

// nodeFillZFSDosFlags records the ZFS DOS flags of the regular file or
// directory open as fd, on device dev, when they aren't archive alone.
func nodeFillZFSDosFlags(node *data.Node, path string, fd int, dev uint64) {
	if !isZFS(fd, dev) {
		return
	}
	var dos uint64
	if err := ioctlDosFlags(fd, zfsIOCGetDosFlags, &dos); err != nil {
		// An OpenZFS without the ioctls (ENOTTY), or the like.
		debug.Log("no ZFS DOS flags for %v: %v", path, err)
		return
	}
	if kept, ok := keptZFSDosFlags(dos); ok {
		node.SetZFSDosFlags(kept)
	}
}

// mergeZFSDosFlags returns current, the ZFS DOS flags a file has, with those
// kept replaced by those in flags: immutable, append-only and nodump, which
// the inode flags set after, and reparse stay as they are.
// ZFS_IOC_SETDOSFLAGS sets the whole mask, so they must be passed again.
func mergeZFSDosFlags(current, flags uint64) uint64 {
	return current&^zfsDosFlags | flags&zfsDosFlags
}

func restoreZFSDosFlags(path string, typ data.NodeType, flags uint64) error {
	fd, err := openForFlags(path, typ, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return &os.PathError{Op: "fstatfs", Path: path, Err: err}
	}
	if uint32(st.Type) != zfsSuperMagic {
		return &UnsupportedError{Attr: zfsDosFlagsAttribute, Err: errNotZFS}
	}
	return restoreZFSDosFlagsWith(path, flags,
		func() (uint64, error) {
			var dos uint64
			err := ioctlDosFlags(fd, zfsIOCGetDosFlags, &dos)
			return dos, err
		},
		func(dos uint64) error { return ioctlDosFlags(fd, zfsIOCSetDosFlags, &dos) })
}

// restoreZFSDosFlagsWith sets the kept ZFS DOS flags of the file at path to
// flags, through get (ZFS_IOC_GETDOSFLAGS) and set (ZFS_IOC_SETDOSFLAGS).
// It leaves flags that are right already alone, with no set at all.  As
// owner (not root), every kept flag can be set; immutable and append-only,
// which would need root, don't change here.
func restoreZFSDosFlagsWith(path string, flags uint64, get func() (uint64, error), set func(uint64) error) error {
	current, err := get()
	if err != nil {
		err = &os.PathError{Op: "ZFS_IOC_GETDOSFLAGS", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: zfsDosFlagsAttribute, Err: err}
		}
		return err
	}
	if mergeZFSDosFlags(current, flags) == current {
		return nil
	}
	own := current &^ zfsDosFlags
	err = changeBitsBestEffort(zfsDosFlagsAttribute, "ZFS DOS flags", current&zfsDosFlags, flags&zfsDosFlags, func(f uint64) error { return set(own | f) })
	if err != nil {
		return &os.PathError{Op: "ZFS_IOC_SETDOSFLAGS", Path: path, Err: err}
	}
	return nil
}
