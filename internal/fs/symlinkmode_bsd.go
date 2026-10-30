//go:build freebsd || openbsd || netbsd || dragonfly

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// nodeRestoreSymlinkMode gives a symlink its own permissions back: the BSDs
// keep them (FreeBSD's, NetBSD's and DragonFly's lchmod(2), and fchmodat(2)
// with AT_SYMLINK_NOFOLLOW everywhere, OpenBSD's only way), and ls(1) and
// mtree(8) show them, though access doesn't depend on them.  Without this a
// restored symlink gets 0755.
func nodeRestoreSymlinkMode(node *data.Node, path string) error {
	return lchmod(path, node.Mode)
}

// lchmod sets the permissions of the file at path without following a
// symlink.  A file system that has no such permissions for symlinks is left
// alone.
func lchmod(path string, mode os.FileMode) error {
	err := unix.Fchmodat(unix.AT_FDCWD, path, uint32(mode.Perm()), unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) {
		debug.Log("no mode for symlink %v: %v", path, err)
		return nil
	}
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	return nil
}
