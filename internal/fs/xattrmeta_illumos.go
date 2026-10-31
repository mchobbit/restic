//go:build illumos

package fs

import (
	"maps"
	"os"
	"slices"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// The owner, group, mode, times and ACL of the extended attributes of a
// regular file or directory (illumos_xattrmeta.go), read through its
// attribute directory, as the values are (xattr_illumos.go).  The values are
// read later, which changes the attributes' atimes, and restored earlier.

// nodeFillXattrMeta records the metadata of each extended attribute of the
// file at path.  An attribute that can't be read is left out, as its value
// is (nodeFillExtendedAttributes warns about that); an ACL that can't be
// read too.
func nodeFillXattrMeta(node *data.Node, path string, sids *sidNamer) {
	dir, err := attrDir(path)
	if dir == nil {
		if err != nil {
			debug.Log("no extended attributes for %v: %v", path, err)
		}
		return
	}
	defer func() { _ = dir.Close() }()
	names, err := attrNames(dir, path)
	if err != nil {
		debug.Log("no extended attributes for %v: %v", path, err)
		return
	}
	metas := make(map[string]xattrMeta, len(names))
	for _, name := range names {
		if meta, ok := readXattrMeta(int(dir.Fd()), path, name, sids); ok {
			metas[name] = meta
		}
	}
	if len(metas) > 0 {
		_ = node.SetGenericAttribute(data.TypeIllumosXattrMeta, metas)
	}
}

// readXattrMeta returns the metadata of the attribute name in dirfd, the
// attribute directory of path, and whether it could read it.
func readXattrMeta(dirfd int, path, name string, sids *sidNamer) (xattrMeta, bool) {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		debug.Log("fstatat %v: %v", attrPath(path, name), err)
		return xattrMeta{}, false
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return xattrMeta{}, false
	}
	meta := xattrMeta{
		UID:   st.Uid,
		GID:   st.Gid,
		Mode:  st.Mode & 0o7777,
		MTime: time.Unix(st.Mtim.Unix()).UTC(),
		ATime: time.Unix(st.Atim.Unix()).UTC(),
	}
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		debug.Log("no ACL for %v: %v", attrPath(path, name), err)
		return meta, true
	}
	defer func() { _ = unix.Close(fd) }()
	entries, err := fdACL(fd, attrPath(path, name)).get()
	if err != nil {
		debug.Log("no ACL for %v: %v", attrPath(path, name), err)
		return meta, true
	}
	if len(entries) > 0 && !aceIsTrivial(entries) {
		sids.aces(entries)
		meta.ACL = entries
	}
	return meta, true
}

// nodeRestoreXattrMeta gives the extended attributes of the file at path,
// restored by now, their metadata.  One without a value in node (none was
// kept, or the restore left it out) is left alone, as is a file system
// without them.  A malformed attribute gives a data.MalformedAttributeError,
// a SID in an ACL that idmap can't map a *SIDWarning.
func nodeRestoreXattrMeta(node *data.Node, path string) error {
	var metas map[string]xattrMeta
	ok, err := node.GenericAttribute(data.TypeIllumosXattrMeta, &metas)
	if err != nil {
		return &data.MalformedAttributeError{Attr: data.TypeIllumosXattrMeta, Err: err}
	}
	if !ok || len(metas) == 0 {
		return nil
	}
	dir, err := attrDir(path)
	if dir == nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	var errs []error
	var warning *SIDWarning
	for _, name := range slices.Sorted(maps.Keys(metas)) {
		if !slices.ContainsFunc(node.ExtendedAttributes, func(attr data.ExtendedAttribute) bool { return attr.Name == name }) {
			continue
		}
		w, err := restoreXattrMeta(int(dir.Fd()), path, name, metas[name])
		errs = append(errs, err)
		if warning == nil {
			warning = w
		}
	}
	if warning != nil {
		// One per file, as for its own ACL.
		errs = append(errs, warning)
	}
	return errors.Join(errs...)
}

// restoreXattrMeta gives the attribute name in dirfd, the attribute
// directory of path, the metadata meta: owner, mode, ACL (after the mode,
// which rewrites it), then the times, last, as for the file itself.  Each
// is tried even when one before it failed.  It returns a warning about a SID
// in the ACL that idmap couldn't map, and the errors.
func restoreXattrMeta(dirfd int, path, name string, meta xattrMeta) (*SIDWarning, error) {
	at := attrPath(path, name)
	var errs []error
	if err := unix.Fchownat(dirfd, name, int(meta.UID), int(meta.GID), 0); err != nil {
		if errors.Is(err, unix.ENOENT) {
			debug.Log("%v not restored: %v", at, err)
			return nil, nil
		}
		errs = append(errs, &os.PathError{Op: "fchownat", Path: at, Err: err})
	}
	if err := unix.Fchmodat(dirfd, name, meta.Mode&0o7777, 0); err != nil {
		errs = append(errs, &os.PathError{Op: "fchmodat", Path: at, Err: err})
	}
	var warning *SIDWarning
	if len(meta.ACL) > 0 {
		warning = acesWithIDs(data.TypeIllumosXattrMeta, meta.ACL)
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			errs = append(errs, &os.PathError{Op: "openat", Path: at, Err: err})
		} else {
			errs = append(errs, fdACL(fd, at).set(meta.ACL))
			_ = unix.Close(fd)
		}
	}
	ts := []unix.Timespec{unix.NsecToTimespec(meta.ATime.UnixNano()), unix.NsecToTimespec(meta.MTime.UnixNano())}
	if err := unix.UtimesNanoAt(dirfd, name, ts, 0); err != nil {
		errs = append(errs, &os.PathError{Op: "utimensat", Path: at, Err: err})
	}
	return warning, errors.Join(errs...)
}
