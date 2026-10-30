//go:build netbsd

package fs

import (
	"os"
	"syscall"

	"github.com/restic/restic/internal/data"
	"golang.org/x/sys/unix"
)

const fileFlagsAttribute = data.TypeNetBSDFileFlags

func nodeFillFileFlags(node *data.Node, _ string, fi *ExtendedFileInfo) error {
	if s, ok := fi.sys.(*syscall.Stat_t); ok {
		node.SetFileFlags(fileFlagsAttribute, s.Flags&netbsdFileFlags)
	}
	return nil
}

func restoreFileFlags(path string, _ data.NodeType, flags uint32, _ []byte) error {
	// Leave the flags that aren't kept as the file has them.
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	own := st.Flags &^ netbsdFileFlags
	return setFlagsBestEffort(flags&netbsdFileFlags, func(f uint32) error {
		return lchflags(path, own|f)
	})
}
