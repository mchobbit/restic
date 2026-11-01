//go:build freebsd

package fs

import (
	"os"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// ZFS project IDs on FreeBSD (illumos_project.go has their form).  OpenZFS
// takes Linux's struct fsxattr there, with ioctls 'X' 31 and 32, as
// truss(1) shows zfs project calling them on FreeBSD 15.1: 0x401c581f and
// 0x801c5820.  Of the flags, only inheritance (zfsProjInherit) is changed;
// the others are given back as read.  A file system other than ZFS (UFS)
// answers ENOTTY.

// zfsxattr is the struct ZFS's project ioctls take, 28 bytes.
type zfsxattr struct {
	xflags     uint32
	extsize    uint32
	nextents   uint32
	projid     uint32
	cowextsize uint32
	pad        [8]byte
}

const (
	zfsIOCFSGetXattr = 0x401c581f // _IOR('X', 31, zfsxattr_t)
	zfsIOCFSSetXattr = 0x801c5820 // _IOW('X', 32, zfsxattr_t)
)

func ioctlZfsxattr(fd int, req uintptr, zx *zfsxattr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(zx)))
	if errno != 0 {
		return errno
	}
	return nil
}

// nodeFillZFSProject records the ZFS project of the regular file or
// directory at path, on ZFS, when it has one: an ID or inheritance.
func nodeFillZFSProject(node *data.Node, path string, fi *ExtendedFileInfo) {
	if node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir || !isZFS(path, node.Type, fi.DeviceID) {
		return
	}
	fd, err := openForFlags(path, node.Type, fi)
	if err != nil {
		debug.Log("no ZFS project for %v: %v", path, err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	var zx zfsxattr
	if err := ioctlZfsxattr(fd, zfsIOCFSGetXattr, &zx); err != nil {
		debug.Log("no ZFS project for %v: %v", path, err)
		return
	}
	if p, ok := zfsProjectOf(zx.xflags, zx.projid); ok {
		_ = node.SetGenericAttribute(data.TypeFreeBSDZFSProject, p)
	}
}

// nodeRestoreZFSProject gives the regular file or directory at path its ZFS
// project: its own ID and inheritance, whatever it got from the directory it
// was created in, as on illumos.  It goes before the file flags.  A file
// system other than ZFS gives an *UnsupportedError, a malformed attribute a
// data.MalformedAttributeError, for the restorer to warn about.
func nodeRestoreZFSProject(node *data.Node, path string) error {
	var p zfsProject
	ok, err := node.GenericAttribute(data.TypeFreeBSDZFSProject, &p)
	if err != nil {
		return &data.MalformedAttributeError{Attr: data.TypeFreeBSDZFSProject, Err: err}
	}
	if !ok || node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir {
		return nil
	}
	fd, err := openForFlags(path, node.Type, nil)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var zx zfsxattr
	if err := ioctlZfsxattr(fd, zfsIOCFSGetXattr, &zx); err != nil {
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: data.TypeFreeBSDZFSProject, Err: err}
		}
		return &os.PathError{Op: "ioctl get ZFS project", Path: path, Err: err}
	}
	want := zx
	want.xflags, want.projid = zfsProjectXflags(zx.xflags, p), p.ID
	if want == zx {
		return nil
	}
	if err := ioctlZfsxattr(fd, zfsIOCFSSetXattr, &want); err != nil {
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: data.TypeFreeBSDZFSProject, Err: err}
		}
		return &os.PathError{Op: "ioctl set ZFS project", Path: path, Err: err}
	}
	return nil
}
