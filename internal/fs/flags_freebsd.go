//go:build freebsd

package fs

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeFreeBSDFileFlags

// From FreeBSD's <sys/stat.h>.
const (
	ufSettable = 0x0000ffff // the flags an owner may set
	sfSettable = 0xffff0000 // the flags only root may set
	sfSnapshot = 0x00200000 // a UFS snapshot file
)

// freebsdFileFlags are the flags kept: all that an owner or root can set,
// which leaves out SF_SNAPSHOT.
const freebsdFileFlags = (ufSettable | sfSettable) &^ sfSnapshot

func nodeFillFileFlags(node *data.Node, path string, fi *ExtendedFileInfo) error {
	s, ok := fi.sys.(*syscall.Stat_t)
	if !ok {
		return nil
	}
	flags := s.Flags & freebsdFileFlags
	if !keptFreeBSDFileFlags(flags, isZFS(path, node.Type, s.Dev)) {
		return nil
	}
	// 0 too on ZFS: archive cleared (SetFileFlags would leave it out).
	_ = node.SetGenericAttribute(fileFlagsAttribute, flags)
	return nil
}

// zfsDevices caches, by device (st_dev), whether a file system is ZFS, for
// the backup to ask statfs(2) once per file system.
var zfsDevices sync.Map

// isZFS tells whether the file at path, of type typ, on device dev, is on
// ZFS.  statfs(2) follows symlinks, so for a symlink it asks about the
// directory the symlink is in, which is on the same file system.
func isZFS(path string, typ data.NodeType, dev uint64) bool {
	if zfs, ok := zfsDevices.Load(dev); ok {
		return zfs.(bool)
	}
	if typ == data.NodeTypeSymlink {
		path = filepath.Dir(path)
	}
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		debug.Log("statfs %v on device %#x: %v", path, dev, err)
		return false
	}
	zfs := unix.ByteSliceToString(st.Fstypename[:]) == "zfs"
	zfsDevices.Store(dev, zfs)
	return zfs
}

func restoreFileFlags(path string, _ data.NodeType, flags uint32, _ []byte) error {
	// Leave the flags that aren't kept as the file has them.
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	own := st.Flags &^ freebsdFileFlags
	return setFlagsBestEffort(flags&freebsdFileFlags, func(f uint32) error {
		return lchflags(path, own|f)
	})
}
