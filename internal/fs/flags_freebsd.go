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

// freebsdFileFlags are the flags kept.  ZFS sets UF_ARCHIVE on every file it
// writes (as Windows does its archive bit), so keeping it would give nearly
// every file an attribute, and a restore onto ZFS sets it again anyway.
// SF_SNAPSHOT can't be set.
const freebsdFileFlags = (ufSettable | sfSettable) &^ (ufArchive | sfSnapshot)

func nodeFillFileFlags(node *data.Node, _ string, fi *ExtendedFileInfo) error {
	if s, ok := fi.sys.(*syscall.Stat_t); ok {
		node.SetFileFlags(fileFlagsAttribute, s.Flags&freebsdFileFlags)
	}
	return nil
}

func restoreFileFlags(path string, _ data.NodeType, flags uint32) error {
	return setFlagsBestEffort(flags&freebsdFileFlags, func(f uint32) error {
		return lchflags(path, f)
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
