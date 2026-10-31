//go:build illumos

package fs

import (
	"os"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// ZFS project IDs on illumos (illumos_project.go).  illumos has no
// FS_IOC_FSGETXATTR; ZFS takes ioctls numbered as Linux's, 'X' 31 and 32,
// but with an 8-byte struct, the xattr flags and the project ID (as truss(1)
// shows zfs project calling _IORN('X', 31, 8)).  Of the flags, ZFS reports
// and sets only inheritance (zfsProjInherit): an immutable or nodump file
// has xflags 0, and keeps both when they are set to 0 (OmniOS r151058).  A
// file system other than ZFS answers ENOTTY.

//go:cgo_import_dynamic libc_ioctl ioctl "libc.so"
//go:linkname procioctl libc_ioctl

var procioctl uintptr

// zfsxattr is the struct ZFS's project ioctls take.
type zfsxattr struct {
	xflags uint32
	projid uint32
}

const (
	zfsIOCFSGetXattr = 0x4008581f // _IORN('X', 31, 8)
	zfsIOCFSSetXattr = 0x80085820 // _IOWN('X', 32, 8)
)

func ioctlZfsxattr(fd int, req uintptr, zx *zfsxattr) error {
	_, _, errno := sysvicall6(uintptr(unsafe.Pointer(&procioctl)), 3, uintptr(fd), req, uintptr(unsafe.Pointer(zx)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// nodeFillZFSProject records the ZFS project of the regular file or
// directory at path, when it has one: an ID or inheritance.
func nodeFillZFSProject(node *data.Node, path string, fi *ExtendedFileInfo) {
	if node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir {
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
		// Not ZFS (ENOTTY).
		debug.Log("no ZFS project for %v: %v", path, err)
		return
	}
	if p, ok := zfsProjectOf(zx.xflags, zx.projid); ok {
		_ = node.SetGenericAttribute(data.TypeIllumosZFSProject, p)
	}
}

// nodeRestoreZFSProject gives the regular file or directory at path its ZFS
// project: its own ID and inheritance, whatever it got from the directory it
// was created in.  It goes before the file flags, as Linux's project ID
// does, though ZFS gives an immutable file one all the same.  A file system
// other than ZFS gives an *UnsupportedError, a malformed attribute a
// data.MalformedAttributeError, for the restorer to warn about.
func nodeRestoreZFSProject(node *data.Node, path string) error {
	var p zfsProject
	ok, err := node.GenericAttribute(data.TypeIllumosZFSProject, &p)
	if err != nil {
		return &data.MalformedAttributeError{Attr: data.TypeIllumosZFSProject, Err: err}
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
			return &UnsupportedError{Attr: data.TypeIllumosZFSProject, Err: err}
		}
		return &os.PathError{Op: "ioctl get ZFS project", Path: path, Err: err}
	}
	want := zfsxattr{xflags: zfsProjectXflags(zx.xflags, p), projid: p.ID}
	if want == zx {
		return nil
	}
	if err := ioctlZfsxattr(fd, zfsIOCFSSetXattr, &want); err != nil {
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: data.TypeIllumosZFSProject, Err: err}
		}
		return &os.PathError{Op: "ioctl set ZFS project", Path: path, Err: err}
	}
	return nil
}
