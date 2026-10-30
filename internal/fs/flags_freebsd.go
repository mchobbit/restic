//go:build freebsd

package fs

import (
	"os"
	"syscall"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeFreeBSDFileFlags

// From FreeBSD's <sys/stat.h>.
const (
	ufSettable = 0x0000ffff // the flags an owner may set
	sfSettable = 0xffff0000 // the flags only root may set
	ufArchive  = 0x00000800 // "uarch"
	sfSnapshot = 0x00200000 // a UFS snapshot file
)

// freebsdFileFlags are the flags kept: all that an owner or root can set,
// which leaves out SF_SNAPSHOT.
const freebsdFileFlags = (ufSettable | sfSettable) &^ sfSnapshot

func nodeFillFileFlags(node *data.Node, _ string, fi *ExtendedFileInfo) error {
	s, ok := fi.sys.(*syscall.Stat_t)
	if !ok {
		return nil
	}
	flags := s.Flags & freebsdFileFlags
	// ZFS sets UF_ARCHIVE on every file it writes, as Windows does its
	// archive bit.  On its own it would give nearly every file an attribute,
	// and a restore onto ZFS sets it again anyway, so it's kept only along
	// with other flags, which are set exactly as they were.
	if flags&^ufArchive == 0 {
		return nil
	}
	node.SetFileFlags(fileFlagsAttribute, flags)
	return nil
}

func restoreFileFlags(path string, _ data.NodeType, flags uint32) error {
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

// lchflags is chflags(2) without following a symlink, which x/sys/unix lacks.
func lchflags(path string, flags uint32) error {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall(unix.SYS_LCHFLAGS, uintptr(unsafe.Pointer(p)), uintptr(flags), 0)
	if errno != 0 {
		return &os.PathError{Op: "lchflags", Path: path, Err: errno}
	}
	return nil
}
