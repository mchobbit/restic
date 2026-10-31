//go:build !windows

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
)

// lchown gives the file at path node's owner and group: their IDs, or with
// lookupByName the IDs their names have here.  A name that is empty or
// unknown here falls back to the ID in the snapshot, never to root.
func lchown(name string, node *data.Node, lookupByName bool) error {
	uid, gid := node.UID, node.GID
	if lookupByName {
		if id, ok := lookupUid(node.User); ok {
			uid = id
		}
		if id, ok := lookupGid(node.Group); ok {
			gid = id
		}
	}

	return os.Lchown(name, int(uid), int(gid))
}

// nodeRestoreGenericAttributes restores the OS's own attributes that go
// before the mode (FreeBSD's system extended attributes, Linux's project
// ID).  The other generic attributes kept on Unix systems are restored
// later: ACLs after the mode (nodeRestoreACLs), ZFS DOS flags and file
// flags after everything else, by the restorer calling RestoreZFSDosFlags
// and RestoreFileFlags; unix.birthtime with the timestamps
// (nodeRestoreBirthTime).  unix.sparse only tells the restorer how to write a
// file's contents.
func nodeRestoreGenericAttributes(node *data.Node, path string, warn func(msg string)) error {
	return errors.Join(nodeRestoreOSAttributes(node, path),
		data.HandleAllUnknownGenericAttributesFound(node.GenericAttributes, warn))
}

// nodeFillGenericAttributes records the file's flags, on the systems that
// keep them, its ACLs and other attributes on FreeBSD and illumos, its birth
// time where the stat has one (FreeBSD, NetBSD; illumos' is read with the
// system attributes), and whether it has holes.
func nodeFillGenericAttributes(node *data.Node, path string, fi *ExtendedFileInfo) error {
	node.SetBirthTime(fi.BirthTime)
	nodeFillSparse(node, path, fi)
	return errors.Join(nodeFillFileFlags(node, path, fi), nodeFillOSAttributes(node, path, fi))
}
