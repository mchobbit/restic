//go:build linux

package fs

import (
	"fmt"
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeLinuxInodeFlags

// From <linux/fs.h>: the inode flags chattr(1) sets, s u c S i a d A m j t D T
// C P F.  The rest belong to the file system (extents, inline data, ...) or
// need more than setting (DAX, verity, encryption).  Casefolding (F) is set
// or cleared as the restorer creates a directory, or finds it empty, since
// ext4 and f2fs only change it on an empty one (prepareDirForFlags); the
// late pass leaves it alone (keptLate).
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
	0x20000000 | // FS_PROJINHERIT_FL  P
	0x40000000 //   FS_CASEFOLD_FL     F

// keptLate are the kept flags that restoreFileFlags sets, last: all but
// casefolding, which only an empty directory takes.
const keptLate = linuxInodeFlags &^ dirFlagsBeforeEntries

// nodeFillFileFlags records the inode flags of a regular file or directory,
// its project ID (attrs_linux.go), its ZFS DOS flags (dosflags_linux.go) and
// whether a regular file has holes (nodeFillSparse elsewhere), all through
// one descriptor.  A file that can't be opened has none of them.
func nodeFillFileFlags(node *data.Node, path string, fi *ExtendedFileInfo) error {
	if node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir {
		return nil
	}
	fd, err := openForFlags(path, node.Type, fi)
	if err != nil {
		// Not readable (not root), or gone: the backup reports that for
		// the contents, if it matters.
		debug.Log("no file flags for %v: %v", path, err)
		return nil
	}
	defer func() { _ = unix.Close(fd) }()
	nodeFillProjectID(node, path, fd)
	nodeFillZFSDosFlags(node, path, fd, fi.DeviceID)
	if node.Type == data.NodeTypeFile {
		hole := func() (int64, error) {
			hole, err := unix.Seek(fd, 0, seekHole)
			if err != nil {
				debug.Log("no SEEK_HOLE for %v: %v", path, err)
			}
			return hole, err
		}
		if hasHoles(fi.Size, fi.Blocks, hole) {
			node.SetSparse()
		}
	}
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		// A file system without inode flags (ENOTTY, EOPNOTSUPP, ...).
		debug.Log("no file flags for %v: %v", path, err)
		return nil
	}
	node.SetFileFlags(fileFlagsAttribute, flags&linuxInodeFlags)
	return nil
}

// mergeInodeFlags returns current, the flags a file has, with those set
// last (keptLate) replaced by those in flags: the file system's own, and
// casefolding, stay as they are.
func mergeInodeFlags(current, flags uint32) uint32 {
	return current&^keptLate | flags&keptLate
}

// The flags FS_IOC_FSSETXATTR also sets, and their fsx_xflags.
const (
	fsImmutableFl     = 0x00000010 // FS_IMMUTABLE_FL
	fsAppendFl        = 0x00000020 // FS_APPEND_FL
	fsXflagImmutable  = 0x00000008 // FS_XFLAG_IMMUTABLE
	fsXflagAppend     = 0x00000010 // FS_XFLAG_APPEND
	fsImmutableAppend = fsImmutableFl | fsAppendFl
)

// inodeFlagOps are the calls restoreFlagsWith makes on a file.
type inodeFlagOps struct {
	get func() (uint32, error) // FS_IOC_GETFLAGS
	set func(uint32) error     // FS_IOC_SETFLAGS
	// setCompression sets the btrfs.compression extended attribute again;
	// nil when the file has none.
	setCompression func() error
	// addXflags adds to the file's fsx_xflags (FS_IOC_FSSETXATTR).
	addXflags func(uint32) error
}

func restoreFileFlags(path string, typ data.NodeType, flags uint32, compression []byte) error {
	fd, err := openForFlags(path, typ, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	ops := inodeFlagOps{
		get: func() (uint32, error) { return unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS) },
		set: func(f uint32) error { return unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(f)) },
		addXflags: func(xflags uint32) error {
			var fsx fsxattr
			if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
				return err
			}
			fsx.xflags |= xflags
			return ioctlFsxattr(fd, fsIOCFSSetXattr, &fsx)
		},
	}
	if compression != nil {
		ops.setCompression = func() error { return setxattr(path, "btrfs.compression", compression) }
	}
	return restoreFlagsWith(path, typ, flags, ops)
}

// restoreFlagsWith sets the kept flags of the file at path, except
// casefolding, to flags, through ops.  It leaves flags that are right
// already alone, with no FS_IOC_SETFLAGS at all.  On a directory whose
// casefolding differs from flags' (prepareDirForFlags found it with
// entries, or the file system can't casefold), it adds a *FlagsWarning.
func restoreFlagsWith(path string, typ data.NodeType, flags uint32, ops inodeFlagOps) error {
	current, err := ops.get()
	if err != nil {
		err = &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: fileFlagsAttribute, Err: err}
		}
		return err
	}
	var errs []error
	if mergeInodeFlags(current, flags) != current {
		errs = append(errs, setLateFlags(path, current, flags, ops))
	}
	if typ == data.NodeTypeDir && (current^flags)&dirFlagsBeforeEntries != 0 {
		errs = append(errs, &FlagsWarning{Msg: fmt.Sprintf("casefolding (F) not restored on %v: directory not empty or not supported", path)})
	}
	return errors.Join(errs...)
}

// setLateFlags sets the file's kept flags but casefolding, which are
// current, to those of flags.
//
// On btrfs every FS_IOC_SETFLAGS rewrites the btrfs.compression property,
// restored before as an extended attribute: zstd and lzo become zlib, and
// "no" goes.  So for a file with that attribute, the flags but immutable and
// append-only are set first, the attribute again after, and immutable and
// append-only last, through FS_IOC_FSSETXATTR, which leaves the property
// alone.
func setLateFlags(path string, current, flags uint32, ops inodeFlagOps) error {
	own := current &^ keptLate
	want := flags & keptLate
	var viaXflags uint32
	if ops.setCompression != nil {
		viaXflags = want & fsImmutableAppend
		want &^= fsImmutableAppend
	}
	var errs []error
	if own|want != current {
		err := setFlagsBestEffort(want, func(f uint32) error { return ops.set(own | f) })
		if err != nil {
			errs = append(errs, &os.PathError{Op: "FS_IOC_SETFLAGS", Path: path, Err: err})
		}
		if ops.setCompression != nil {
			if err := ops.setCompression(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if viaXflags != 0 {
		var xflags uint32
		if viaXflags&fsImmutableFl != 0 {
			xflags |= fsXflagImmutable
		}
		if viaXflags&fsAppendFl != 0 {
			xflags |= fsXflagAppend
		}
		if err := ops.addXflags(xflags); err != nil {
			err = &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}
			if unsupportedErr(err) {
				err = &UnsupportedError{Attr: fileFlagsAttribute, Bits: uint64(viaXflags), Err: err}
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// flagsBeforeData are the flags a file system only takes while a file is
// empty: btrfs ignores FS_NOCOW_FL on a file with data, without an error.
const flagsBeforeData = 0x00800000 // FS_NOCOW_FL

// dirFlagsBeforeEntries are the flags a file system only changes while a
// directory is empty: ext4 and f2fs refuse to set or clear FS_CASEFOLD_FL
// on one with entries (ENOTEMPTY).
const dirFlagsBeforeEntries = 0x40000000 // FS_CASEFOLD_FL

func prepareFileForFlags(path string, flags uint32) error {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if errors.Is(err, unix.EEXIST) {
		return nil // the restorer decides what to do with an existing file
	}
	if err != nil {
		return &os.PathError{Op: "create", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	return changeFlagsEarly(fd, path, func(current uint32) uint32 { return current | flags })
}

// prepareDirForFlags makes the empty directory's casefolding that of flags:
// it sets it, or clears it where the directory inherited it.
func prepareDirForFlags(path string, flags uint32) error {
	fd, err := openForFlags(path, data.NodeTypeDir, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return changeFlagsEarly(fd, path, func(current uint32) uint32 {
		return current&^dirFlagsBeforeEntries | flags
	})
}

// changeFlagsEarly sets the flags of the empty file or directory open as fd
// to change(current), unless they are that already.  A file system without
// the flags is left alone, silently: where it matters, restoreFileFlags
// reports that at the end.
func changeFlagsEarly(fd int, path string, change func(current uint32) uint32) error {
	current, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		if unsupportedErr(err) {
			return nil // no inode flags here
		}
		return &os.PathError{Op: "FS_IOC_GETFLAGS", Path: path, Err: err}
	}
	wanted := change(current)
	if wanted == current {
		return nil // inherited from the parent directory, or none
	}
	if err := unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(wanted)); err != nil {
		if unsupportedErr(err) {
			return nil
		}
		return &os.PathError{Op: "FS_IOC_SETFLAGS", Path: path, Err: err}
	}
	return nil
}
