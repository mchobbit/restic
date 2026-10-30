//go:build !windows

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
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

// nodeRestoreGenericAttributes restores FreeBSD's system extended attributes.
// The other generic attributes kept on Unix systems are restored later: ACLs
// after the mode (nodeRestoreACLs), file flags after everything else, by the
// restorer calling RestoreFileFlags.
func nodeRestoreGenericAttributes(node *data.Node, path string, warn func(msg string)) error {
	return errors.Join(nodeRestoreFreeBSDExtattrs(node, path),
		data.HandleAllUnknownGenericAttributesFound(node.GenericAttributes, warn))
}

// nodeFillGenericAttributes records the file's flags, on the systems that
// keep them, and on FreeBSD its system extended attributes and ACLs.
func nodeFillGenericAttributes(node *data.Node, path string, fi *ExtendedFileInfo) error {
	return errors.Join(nodeFillFileFlags(node, path, fi), nodeFillFreeBSDAttributes(node, path, fi))
}
