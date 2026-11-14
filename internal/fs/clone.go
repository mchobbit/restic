package fs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/restic/restic/internal/errors"
)

// CloneError is CloneFile's error.  Touched tells whether the destination
// was made or emptied before the clone failed: if not, it is as it was.
type CloneError struct {
	Touched bool
	Err     error
}

func (e *CloneError) Error() string {
	return e.Err.Error()
}

func (e *CloneError) Unwrap() error {
	return e.Err
}

// CloneFile gives the regular file dst (made, or emptied) the whole contents
// of the regular file src, sharing its extents (Linux's FICLONE), where the
// file system can: restore --clone-from, and --exact breaking a hard link.
// src must hold size bytes.  dst is touched only once src is open and that
// size, and when it isn't src itself (by another name too): emptying it
// would empty src, and a file system that clones then clones nothing and
// succeeds (btrfs, XFS), leaving an empty file.  Nor is a dst with other
// names touched, which would all get the contents.  Its metadata is a new
// file's; the restorer sets it after.  The error is a *CloneError.  As in
// rustic-bko (exact::clone_file).
func CloneFile(src, dst string, size int64) error {
	untouched := func(err error) error { return &CloneError{Err: err} }
	s, err := OpenFile(src, O_RDONLY|O_NOFOLLOW, 0)
	if err != nil {
		return untouched(err)
	}
	defer func() { _ = s.Close() }()
	sfi, err := s.Stat()
	if err != nil {
		return untouched(err)
	}
	if !sfi.Mode().IsRegular() || sfi.Size() != size {
		return untouched(fmt.Errorf("%v isn't a regular file of %d bytes", src, size))
	}
	if dfi, err := Lstat(dst); err == nil {
		if os.SameFile(sfi, dfi) {
			return untouched(fmt.Errorf("%v is %v itself", src, dst))
		}
		if dfi.Mode().IsRegular() && ExtendedStat(dfi).Links > 1 {
			return untouched(fmt.Errorf("%v has other names", dst))
		}
	}
	d, err := OpenFile(dst, O_CREATE|O_WRONLY|O_TRUNC|O_NOFOLLOW, 0o600)
	if err != nil {
		return untouched(err)
	}
	err = cloneContents(d, s)
	err = errors.Join(err, d.Close())
	if err != nil {
		return &CloneError{Touched: true, Err: err}
	}
	return nil
}

// BreakHardLink gives the existing regular file path, which has other names,
// a file of its own, so that no write to it reaches them: a clone of it
// (CloneFile to a temporary file in the same directory, renamed over it),
// which writes nothing and keeps sharing its extents, or, where the file
// system can't clone, nothing: path is removed, for the restore to write
// whole.  The other names keep the old file.  It tells whether path is a
// clone.  The clone's metadata is a new file's; the restorer sets the
// node's after.  As in rustic-bko (exact::break_link).
func BreakHardLink(path string) (cloned bool, err error) {
	tmp := filepath.Join(filepath.Dir(path), ".bko-exact-"+filepath.Base(path))
	err = func() error {
		fi, err := Lstat(path)
		if err != nil {
			return err
		}
		// CloneFile refuses a destination with other names, not a source.
		if err := CloneFile(path, tmp, fi.Size()); err != nil {
			return err
		}
		return os.Rename(fixpath(tmp), fixpath(path))
	}()
	if err == nil {
		return true, nil
	}
	_ = Remove(tmp)
	if err := Remove(path); err != nil {
		return false, err
	}
	return false, nil
}
