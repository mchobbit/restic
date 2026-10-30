//go:build !windows

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
)

func lchown(name string, node *data.Node, lookupByName bool) error {
	var uid, gid uint32
	if lookupByName {
		uid = lookupUid(node.User)
		gid = lookupGid(node.Group)
	} else {
		uid = node.UID
		gid = node.GID
	}

	return os.Lchown(name, int(uid), int(gid))
}

// nodeRestoreGenericAttributes is no-op.  File flags, the one generic
// attribute kept on some Unix systems, are restored later, by the restorer
// calling RestoreFileFlags.
func nodeRestoreGenericAttributes(node *data.Node, _ string, warn func(msg string)) error {
	return data.HandleAllUnknownGenericAttributesFound(node.GenericAttributes, warn)
}

// nodeFillGenericAttributes records the file's flags, on the systems that
// keep them.
func nodeFillGenericAttributes(node *data.Node, path string, fi *ExtendedFileInfo) error {
	return nodeFillFileFlags(node, path, fi)
}
