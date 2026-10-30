//go:build openbsd

package fs

import (
	"os"
	"syscall"

	"github.com/restic/restic/internal/data"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeOpenBSDFileFlags

// From OpenBSD's <sys/stat.h>: the flags an owner may set (nodump,
// immutable, append, opaque) and those only root may (archived, immutable,
// append; the last two can't be cleared again above securelevel 0).
const openbsdFileFlags = 0x0000ffff | 0xffff0000

func nodeFillFileFlags(node *data.Node, _ string, fi *ExtendedFileInfo) error {
	if s, ok := fi.sys.(*syscall.Stat_t); ok {
		node.SetFileFlags(fileFlagsAttribute, s.Flags&openbsdFileFlags)
	}
	return nil
}

func restoreFileFlags(path string, typ data.NodeType, flags uint32, _ []byte) error {
	set := func(f uint32) error {
		var err error
		if typ == data.NodeTypeSymlink {
			err = lchflags(path, f)
		} else {
			err = unix.Chflags(path, int(f))
		}
		if err != nil {
			return &os.PathError{Op: "chflags", Path: path, Err: err}
		}
		return nil
	}
	return setFlagsBestEffort(flags&openbsdFileFlags, set)
}
