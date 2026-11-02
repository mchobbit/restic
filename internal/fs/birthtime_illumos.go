//go:build illumos

package fs

import (
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// nodeRestoreBirthTime gives a regular file or directory its birth time,
// ZFS's crtime, which can be set to any time: an nvlist with crtime alone,
// written to its SUNWattr_rw view, sets it and leaves everything else.  It
// goes before the system attributes, since an immutable file refuses it.
// Symlinks have no view of their own (it is their target's), the other
// types none, and UFS has none.  tmpfs has one, but refuses a crtime
// (birthTimeUnsupported).
func nodeRestoreBirthTime(node *data.Node, path string) error {
	birth, ok, err := node.BirthTime()
	if !ok || err != nil {
		return err
	}
	f, err := sysattrView(path, node.Type, true)
	if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		debug.Log("no birth time for %v: %v", path, err)
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteAt(crtimeNvlist(birth), 0)
	if birthTimeUnsupported(err) {
		debug.Log("no birth time for %v: %v", path, err)
		return nil
	}
	if err != nil {
		return &os.PathError{Op: "write SUNWattr_rw crtime", Path: path, Err: err}
	}
	return nil
}

// birthTimeUnsupported tells whether err, from writing a crtime to a
// file's SUNWattr_rw view, says its file system keeps none it can be given:
// illumos' tmpfs has the view, but refuses a crtime with EINVAL.  As in
// rustic-bko, that is no error, nor a warning: as on UFS, which has no view.
func birthTimeUnsupported(err error) bool {
	return errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS)
}
