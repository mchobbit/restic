//go:build dragonfly

package fs

import (
	"syscall"

	"github.com/restic/restic/internal/data"
)

const fileFlagsAttribute = data.TypeDragonFlyFileFlags

// restrictingFlags are the flags ClearRestrictingFlags clears here (bsd_flags.go).
const restrictingFlags = dragonflyRestrictingFlags

func nodeFillFileFlags(node *data.Node, _ string, fi *ExtendedFileInfo) error {
	if s, ok := fi.sys.(*syscall.Stat_t); ok {
		node.SetFileFlags(fileFlagsAttribute, s.Flags&dragonflyFileFlags)
	}
	return nil
}

func restoreFileFlags(path string, _ data.NodeType, flags uint32, _ []byte) error {
	// Every flag is kept, so there are none of the file's own to leave.
	return setFlagsBestEffort(flags&dragonflyFileFlags, func(f uint32) error {
		return lchflags(path, f)
	})
}
