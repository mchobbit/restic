//go:build linux

package fs

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// Linux metadata that restic otherwise misses, besides the inode flags
// (flags_linux.go): a file's project ID, which project quotas on XFS, ext4,
// f2fs and bcachefs count its blocks against (chattr -p, xfs_quota project
// -s).
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

// bcachefsSuperMagic is BCACHEFS_SUPER_MAGIC, statfs(2)'s f_type on
// bcachefs.
const bcachefsSuperMagic = 0xca451a4e

// bcachefsProject is the extended attribute that shows a bcachefs file's
// project ID.
const bcachefsProject = bcachefsEffective + "project"

// fsTypes caches, by device (st_dev), a file system's statfs(2) f_type, for
// the backup to ask fstatfs(2) once per file system.
var fsTypes sync.Map

// isOn tells whether the file open as fd, on device dev, is on the file
// system whose statfs(2) f_type is magic.
func isOn(fd int, dev uint64, magic uint32) bool {
	if typ, ok := fsTypes.Load(dev); ok {
		return typ.(uint32) == magic
	}
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		debug.Log("fstatfs on device %#x: %v", dev, err)
		return false
	}
	typ := uint32(st.Type)
	fsTypes.Store(dev, typ)
	return typ == magic
}

// isBcachefsFd tells whether the file open as fd is on bcachefs, without
// the cache: for a restore, which has no device number at hand.
func isBcachefsFd(fd int) bool {
	var st unix.Statfs_t
	return unix.Fstatfs(fd, &st) == nil && uint32(st.Type) == bcachefsSuperMagic
}

// fileProjectID returns the project ID of the file open as fd, whose
// FS_IOC_FSGETXATTR gave projid.  bcachefs (Linux 6.12) gives there the ID
// the file had when its inode was read in, 0 for one set since (chattr -p,
// a restore), until it is read in again; the ID it has is in the extended
// attribute bcachefs_effective.project, which is taken where there is one,
// as rustic-bko does.
func fileProjectID(fd int, projid uint32, bcachefs bool) uint32 {
	if !bcachefs {
		return projid
	}
	buf := make([]byte, 32)
	n, err := unix.Fgetxattr(fd, bcachefsProject, buf)
	if err != nil {
		debug.Log("no %v: %v", bcachefsProject, err)
		return projid
	}
	if id, ok := parseBcachefsProject(buf[:n]); ok {
		return id
	}
	return projid
}

// parseBcachefsProject reads the value of bcachefs_effective.project, a
// number in decimal, and whether it is one.
func parseBcachefsProject(value []byte) (uint32, bool) {
	s := strings.TrimSpace(strings.TrimRight(string(value), "\x00"))
	id, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(id), true
}

// nodeFillProjectID records the project ID of the regular file or directory
// open as fd, on device dev, when it isn't 0: on bcachefs, the one in
// bcachefs_effective.project (fileProjectID).
func nodeFillProjectID(node *data.Node, path string, fd int, dev uint64) {
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		// A file system without project IDs (ENOTTY, EOPNOTSUPP, EINVAL).
		debug.Log("no project ID for %v: %v", path, err)
		return
	}
	node.SetProjectID(fileProjectID(fd, fsx.projid, isOn(fd, dev, bcachefsSuperMagic)))
}

// nodeFillOSAttributes has nothing to add on Linux: nodeFillFileFlags reads
// the project ID along with the flags.
func nodeFillOSAttributes(_ *data.Node, _ string, _ *ExtendedFileInfo) error {
	return nil
}

// nodeRestoreOSAttributes restores the project ID, with the extended
// attributes, before the file flags: an immutable file refuses a new one.
// Only regular files and directories have one, here as at backup: a
// symlink, FIFO, socket or device can't be opened for FS_IOC_FSSETXATTR
// without following it or side effects.  A file system without project IDs
// gives an *UnsupportedError, a malformed one a
// data.MalformedAttributeError, for the restorer to warn about.  On
// bcachefs a file without one gets 0, where it inherited another from its
// directory (whose project ID the restore gives it before its entries,
// PrepareDirForOptions); elsewhere a project ID of 0 is left as it is.
func nodeRestoreOSAttributes(node *data.Node, path string) error {
	id, ok, err := node.ProjectID()
	if err != nil {
		return err
	}
	if node.Type != data.NodeTypeFile && node.Type != data.NodeTypeDir {
		return nil
	}
	if !ok && !isBcachefsPath(path) {
		return nil
	}
	fd, err := openForFlags(path, node.Type, nil)
	if err != nil {
		if !ok {
			debug.Log("project ID of %v not checked: %v", path, err)
			return nil
		}
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	err = restoreProjectID(fd, path, id)
	if err != nil && !ok {
		debug.Log("project ID 0 not restored on %v: %v", path, err)
		return nil
	}
	return err
}

// restoreProjectID gives the regular file or directory open as fd, at path,
// the project ID id, unless it has it already (fileProjectID).
func restoreProjectID(fd int, path string, id uint32) error {
	unsupported := func(err error) error {
		if unsupportedErr(err) {
			return &UnsupportedError{Attr: data.TypeLinuxProjectID, Err: err}
		}
		return err
	}
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		return unsupported(&os.PathError{Op: "FS_IOC_FSGETXATTR", Path: path, Err: err})
	}
	if fileProjectID(fd, fsx.projid, isBcachefsFd(fd)) == id {
		return nil
	}
	fsx.projid = id
	if err := ioctlFsxattr(fd, fsIOCFSSetXattr, &fsx); err != nil {
		return unsupported(&os.PathError{Op: "FS_IOC_FSSETXATTR", Path: path, Err: err})
	}
	return nil
}

// isBcachefsPath tells whether path (followed, if a symlink) is on
// bcachefs.
func isBcachefsPath(path string) bool {
	var st unix.Statfs_t
	return unix.Statfs(path, &st) == nil && uint32(st.Type) == bcachefsSuperMagic
}

// bcachefsOption tells whether the extended attribute name is one of a
// bcachefs file's own options (bcachefs.compression, .data_replicas,
// .background_target, .nocow and the rest) that the restore sets.
func bcachefsOption(name string) bool {
	return strings.HasPrefix(name, "bcachefs.") && xattrKept(name)
}

// PrepareDirForOptions gives the directory at path, which the restore has
// just created or found empty, the options its node has that bcachefs
// passes to the entries a directory gets after they are set: its own
// bcachefs.* options (those xattrSelectFilter selects) and its project ID,
// 0 where it has none.  Its entries then inherit them as in the original,
// so their data is stored as it was (compressed, replicated, on the same
// targets) and their project ID comes back inherited.  Only on bcachefs;
// errors are left for the directory's metadata, which sets them all again,
// to report.
func PrepareDirForOptions(path string, node *data.Node, xattrSelectFilter func(xattrName string) bool) {
	if node == nil || node.Type != data.NodeTypeDir || !isBcachefsPath(path) {
		return
	}
	fd, err := openForFlags(path, data.NodeTypeDir, nil)
	if err != nil {
		debug.Log("options of %v not set early: %v", path, err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	for _, attr := range node.ExtendedAttributes {
		if !bcachefsOption(attr.Name) || !xattrSelectFilter(attr.Name) {
			continue
		}
		if err := unix.Fsetxattr(fd, attr.Name, attr.Value, 0); err != nil {
			debug.Log("%v of %v not set early: %v", attr.Name, path, err)
		}
	}
	id, _, err := node.ProjectID()
	if err != nil {
		return
	}
	if err := restoreProjectID(fd, path, id); err != nil {
		debug.Log("project ID of %v not set early: %v", path, err)
	}
}

func nodeRestoreACLs(_ *data.Node, _ string) error {
	return nil
}

// A symlink's own permissions don't matter on Linux, which has none.
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
