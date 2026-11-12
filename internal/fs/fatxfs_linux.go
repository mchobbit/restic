//go:build linux

package fs

import (
	"os"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// Two file systems keep per-file metadata of their own that neither the
// inode flags nor the project ID carry: FAT (vfat, msdos) its attributes,
// and XFS its allocation options.  Both are kept in generic attributes of
// their own, read through the descriptor opened for the inode flags, and
// restored in the late pass, before the inode flags, as the ZFS DOS flags
// are (dosflags_linux.go).

const (
	fatAttributesAttribute = data.TypeLinuxFATAttributes
	xfsOptionsAttribute    = data.TypeLinuxXFSOptions
)

// FAT_IOCTL_GET_ATTRIBUTES and FAT_IOCTL_SET_ATTRIBUTES from
// <linux/msdos_fs.h>, _IOR('r', 0x10, __u32) and _IOW('r', 0x11, __u32):
// 0x80047210 and 0x40047211 on x86, ARM and RISC-V, with the direction bits
// of FS_IOC_GETFLAGS and FS_IOC_SETFLAGS, as for fsIOCFSGetXattr.  Only
// issued on FAT (msdosSuperMagic): 'r' 0x10 may mean something else to
// another file system.
const (
	fatIOCGetAttributes uint = unix.FS_IOC_GETFLAGS&0xc0000000 | 4<<16 | 'r'<<8 | 0x10
	fatIOCSetAttributes uint = unix.FS_IOC_SETFLAGS&0xc0000000 | 4<<16 | 'r'<<8 | 0x11
)

// msdosSuperMagic is MSDOS_SUPER_MAGIC, statfs(2)'s f_type on vfat and msdos.
const msdosSuperMagic = 0x4d44

// The FAT attributes kept: those FAT_IOCTL_SET_ATTRIBUTES sets.  Volume label
// and directory can't change.  Linux gives a new file archive alone and a
// new directory none, and sets archive again on every write.  A regular
// file's read-only is its mode's write bits as well (fmask permitting); a
// directory's isn't (without the rodir mount option), so it is kept there
// alone.
const (
	fatReadonly = 0x01 // ATTR_RO
	fatHidden   = 0x02 // ATTR_HIDDEN
	fatSystem   = 0x04 // ATTR_SYS
	fatArchive  = 0x20 // ATTR_ARCH
	fatKept     = fatReadonly | fatHidden | fatSystem | fatArchive
)

// errNotFAT is the reason FAT attributes aren't restored on a file system
// other than FAT.
var errNotFAT = errors.New("not FAT")

// keptFATAttributes returns those of attrs, the FAT attributes of a regular
// file or (dir) directory, that a node keeps, and whether it gets them: not
// when they are what the file would have anyway if it were restored without
// them, so that a tree of ordinary files on FAT gets no attribute: archive
// alone for a regular file (read-only or not: its mode says which), none
// for a directory.  0 is kept: a file whose archive was cleared.
func keptFATAttributes(attrs uint32, dir bool) (uint32, bool) {
	kept := attrs & fatKept
	if dir {
		return kept, kept != 0
	}
	return kept, kept&^fatReadonly != fatArchive
}

// mergeFATAttributes returns current, the FAT attributes a file has, with
// the kept ones replaced by those in attrs.
func mergeFATAttributes(current, attrs uint32) uint32 {
	return current&^fatKept | attrs&fatKept
}

func ioctlUint32(fd int, req uint, v *uint32) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(req), uintptr(unsafe.Pointer(v)))
	if errno != 0 {
		return errno
	}
	return nil
}

// nodeFillFATAttributes records the FAT attributes of the regular file or
// directory open as fd, on device dev, when they are worth an attribute
// (keptFATAttributes).
func nodeFillFATAttributes(node *data.Node, path string, fd int, dev uint64) {
	if !isOn(fd, dev, msdosSuperMagic) {
		return
	}
	var attrs uint32
	if err := ioctlUint32(fd, fatIOCGetAttributes, &attrs); err != nil {
		debug.Log("no FAT attributes for %v: %v", path, err)
		return
	}
	if kept, ok := keptFATAttributes(attrs, node.Type == data.NodeTypeDir); ok {
		node.SetFATAttributes(kept)
	}
}

func restoreFATAttributes(path string, typ data.NodeType, attrs uint32) error {
	fd, err := openForFlags(path, typ, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return &os.PathError{Op: "fstatfs", Path: path, Err: err}
	}
	if uint32(st.Type) != msdosSuperMagic {
		return &UnsupportedError{Attr: fatAttributesAttribute, Err: errNotFAT}
	}
	return restoreFATAttributesWith(path, attrs,
		func() (uint32, error) {
			var a uint32
			err := ioctlUint32(fd, fatIOCGetAttributes, &a)
			return a, err
		},
		func(a uint32) error { return ioctlUint32(fd, fatIOCSetAttributes, &a) })
}

// restoreFATAttributesWith gives the file at path the kept FAT attributes
// attrs, through get (FAT_IOCTL_GET_ATTRIBUTES) and set
// (FAT_IOCTL_SET_ATTRIBUTES), which also sets the mode's write bits from
// read-only, as a chmod(2) would.  Attributes that are right already are
// left alone, with no set at all.  The file system's root directory takes
// none (EINVAL), which is reported as unsupported.
func restoreFATAttributesWith(path string, attrs uint32, get func() (uint32, error), set func(uint32) error) error {
	current, err := get()
	if err != nil {
		err = &os.PathError{Op: "FAT_IOCTL_GET_ATTRIBUTES", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: fatAttributesAttribute, Err: err}
		}
		return err
	}
	want := mergeFATAttributes(current, attrs)
	if want == current {
		return nil
	}
	if err := set(want); err != nil {
		err = &os.PathError{Op: "FAT_IOCTL_SET_ATTRIBUTES", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: fatAttributesAttribute, Err: err}
		}
		return err
	}
	return nil
}

// xfsSuperMagic is XFS_SUPER_MAGIC, statfs(2)'s f_type on XFS.
const xfsSuperMagic = 0x58465342

// The fsx_xflags kept on XFS, from <linux/fs.h>: those of a file's options
// that linux.inode_flags doesn't carry (it has immutable, append-only, sync,
// noatime, nodump and project inheritance) and that a file can be given.
// Realtime inheritance only on directories, the only files XFS gives it
// (xfs_flags2diflags), and that take it without a realtime device; not
// realtime itself, which a regular file only gets on one, nor DAX; not
// preallocated (p), which a restore that preallocates (S5) gives every file
// it writes dense, and none it writes sparse.
const (
	xfsXflagRtinherit    = 0x00000100 // FS_XFLAG_RTINHERIT    t (directories)
	xfsXflagNosymlinks   = 0x00000400 // FS_XFLAG_NOSYMLINKS   n
	xfsXflagExtsize      = 0x00000800 // FS_XFLAG_EXTSIZE      e
	xfsXflagExtszinherit = 0x00001000 // FS_XFLAG_EXTSZINHERIT E
	xfsXflagNodefrag     = 0x00002000 // FS_XFLAG_NODEFRAG     f
	xfsXflagFilestream   = 0x00004000 // FS_XFLAG_FILESTREAM   S
	xfsXflagCowextsize   = 0x00010000 // FS_XFLAG_COWEXTSIZE   C
	xfsKept              = xfsXflagNosymlinks | xfsXflagExtsize | xfsXflagExtszinherit |
		xfsXflagNodefrag | xfsXflagFilestream | xfsXflagCowextsize
	xfsKeptDir = xfsKept | xfsXflagRtinherit
)

// xfsKeptFor returns the fsx_xflags kept on a directory when dir is true,
// on a regular file otherwise.
func xfsKeptFor(dir bool) uint32 {
	if dir {
		return xfsKeptDir
	}
	return xfsKept
}

// errNotXFS is the reason XFS options aren't restored on a file system
// other than XFS.
var errNotXFS = errors.New("not XFS")

// keptXFSOptions returns the XFS options of a file whose FS_IOC_FSGETXATTR
// gave fsx, a directory when dir is true, and whether it has any: the kept
// xflags, with the extent size hint where extsize or its inheritance is
// set, and the copy-on-write one where cowextsize is.
func keptXFSOptions(fsx fsxattr, dir bool) (data.XFSOptions, bool) {
	opts := data.XFSOptions{XFlags: fsx.xflags & xfsKeptFor(dir)}
	if opts.XFlags == 0 {
		return data.XFSOptions{}, false
	}
	if opts.XFlags&(xfsXflagExtsize|xfsXflagExtszinherit) != 0 {
		opts.ExtSize = fsx.extsize
	}
	if opts.XFlags&xfsXflagCowextsize != 0 {
		opts.CowExtSize = fsx.cowextsize
	}
	return opts, true
}

// wantedFsxattr returns cur, the fsxattr of a file, a directory when dir is
// true, with the options opts in place of its own: the kept xflags, and the
// hints' sizes, 0 where their flags are off (XFS refuses a size without its
// flag).  The rest (other flags, the project ID) stays as it is; so does a
// regular file's, for realtime inheritance, which only directories take.
func wantedFsxattr(cur fsxattr, opts data.XFSOptions, dir bool) fsxattr {
	kept := xfsKeptFor(dir)
	w := cur
	w.xflags = cur.xflags&^kept | opts.XFlags&kept
	w.extsize, w.cowextsize = 0, 0
	if w.xflags&(xfsXflagExtsize|xfsXflagExtszinherit) != 0 {
		w.extsize = opts.ExtSize
	}
	if w.xflags&xfsXflagCowextsize != 0 {
		w.cowextsize = opts.CowExtSize
	}
	return w
}

// sameXFSOptions tells whether a and b, two fsxattr, give a file the same
// options.
func sameXFSOptions(a, b fsxattr) bool {
	return a.xflags == b.xflags && a.extsize == b.extsize && a.cowextsize == b.cowextsize
}

// nodeFillXFSOptions records the XFS options of the regular file or
// directory open as fd, on device dev, when it has any.
func nodeFillXFSOptions(node *data.Node, path string, fd int, dev uint64) {
	if !isOn(fd, dev, xfsSuperMagic) {
		return
	}
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		debug.Log("no XFS options for %v: %v", path, err)
		return
	}
	if opts, ok := keptXFSOptions(fsx, node.Type == data.NodeTypeDir); ok {
		node.SetXFSOptions(opts)
	}
}

// setXFSOptions gives the file open as fd, at path, a directory when dir is
// true, the XFS options opts, unless it has them already.
func setXFSOptions(fd int, path string, dir bool, opts data.XFSOptions) error {
	var cur fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &cur); err != nil {
		err = &os.PathError{Op: "FS_IOC_FSGETXATTR", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: xfsOptionsAttribute, Err: err}
		}
		return err
	}
	want := wantedFsxattr(cur, opts, dir)
	if sameXFSOptions(cur, want) {
		return nil
	}
	if err := ioctlFsxattr(fd, fsIOCFSSetXattr, &want); err != nil {
		// XFS refuses (EINVAL) to change the extent size hint of a
		// regular file that has data: one restored over an existing file.
		// It refuses a directory realtime inheritance along with the
		// inheritance of an extent size hint that isn't a multiple of
		// its realtime extent size (xfs_ioctl_setattr_check_extsize):
		// the directory then gets the rest, and only realtime
		// inheritance is unsupported.
		if dir && errors.Is(err, unix.EINVAL) && want.xflags&xfsXflagRtinherit != 0 {
			rest := want
			rest.xflags &^= xfsXflagRtinherit
			if ioctlFsxattr(fd, fsIOCFSSetXattr, &rest) == nil {
				return &UnsupportedError{Attr: xfsOptionsAttribute, Bits: xfsXflagRtinherit,
					Err: &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}}
			}
		}
		err = &os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err}
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: xfsOptionsAttribute, Err: err}
		}
		return err
	}
	return nil
}

func restoreXFSOptions(path string, typ data.NodeType, opts data.XFSOptions) error {
	fd, err := openForFlags(path, typ, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return &os.PathError{Op: "fstatfs", Path: path, Err: err}
	}
	if uint32(st.Type) != xfsSuperMagic {
		return &UnsupportedError{Attr: xfsOptionsAttribute, Err: errNotXFS}
	}
	return setXFSOptions(fd, path, typ == data.NodeTypeDir, opts)
}

// clearDirXFSOptions takes the kept XFS options off the existing directory
// at path, which the restore is about to put entries in, so that none of
// them inherits any (they all pass to entries made after them), and returns
// those it had, if it took any off.  Only on XFS; an error leaves the
// directory as it is.
func clearDirXFSOptions(path string) (data.XFSOptions, bool) {
	fd, err := openForFlags(path, data.NodeTypeDir, nil)
	if err != nil {
		debug.Log("XFS options of %v not cleared: %v", path, err)
		return data.XFSOptions{}, false
	}
	defer func() { _ = unix.Close(fd) }()
	var sfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &sfs); err != nil || uint32(sfs.Type) != xfsSuperMagic {
		return data.XFSOptions{}, false
	}
	var cur fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &cur); err != nil {
		debug.Log("XFS options of %v not cleared: %v", path, err)
		return data.XFSOptions{}, false
	}
	had, ok := keptXFSOptions(cur, true)
	if !ok {
		return data.XFSOptions{}, false
	}
	none := wantedFsxattr(cur, data.XFSOptions{}, true)
	if err := ioctlFsxattr(fd, fsIOCFSSetXattr, &none); err != nil {
		debug.Log("XFS options of %v not cleared: %v", path, err)
		return data.XFSOptions{}, false
	}
	return had, true
}

// prepareFileForXFSOptions gives the regular file at path the XFS options
// opts while it is empty: XFS only takes an extent size hint on a regular
// file without data.  It creates the file (empty, mode 0600, for the
// restorer to write into) when there is none, as prepareFileForFlags does,
// and leaves a file with data alone (the late pass reports what it can't
// set then).  Not XFS, or any error, is left for the late pass to report.
func prepareFileForXFSOptions(path string, opts data.XFSOptions) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if errors.Is(err, unix.EEXIST) {
		// An empty file prepareFileForFlags made, or one the restore
		// goes over.
		fd, err = openForFlags(path, data.NodeTypeFile, nil)
	}
	if err != nil {
		debug.Log("XFS options of %v not set early: %v", path, err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Size != 0 {
		return
	}
	var sfs unix.Statfs_t
	if err := unix.Fstatfs(fd, &sfs); err != nil || uint32(sfs.Type) != xfsSuperMagic {
		return
	}
	if err := setXFSOptions(fd, path, false, opts); err != nil {
		debug.Log("XFS options of %v not set early: %v", path, err)
	}
}
