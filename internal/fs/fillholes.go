//go:build linux

package fs

import (
	"errors"
	"os"

	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// fileHoles tells whether the regular file at path, of size bytes, has holes
// that FillHoles can fill, by BackOtter's rule S3 (hasHoles): its lstat
// shows fewer blocks than its size, and then, opened read-only, SEEK_HOLE
// finds a hole (a file on a compressed file system has few blocks and no
// holes).  Never on ZFS, where fallocate(2) allocates nothing, nor where
// nothing is to be allocated (allocates: btrfs but for a NOCOW file,
// bcachefs), so that such a file isn't opened for writing for nothing, nor
// counted by --dry-run.  It returns
// the lstat for FillHoles to check the file against.  An unchanged file
// without holes is only lstat'ed; one with few blocks is opened read-only,
// which gives no close-write event and no ETXTBSY.
func fileHoles(path string, size int64) (unix.Stat_t, bool) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return st, false
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || size <= 0 || st.Blocks*512 >= size {
		return st, false
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		debug.Log("holes of %v not checked: %v", path, err)
		return st, false
	}
	defer func() { _ = unix.Close(fd) }()
	var fst unix.Stat_t
	if err := unix.Fstat(fd, &fst); err != nil || fst.Ino != st.Ino || fst.Dev != st.Dev {
		debug.Log("holes of %v not checked: replaced (%v)", path, err)
		return st, false
	}
	dev := uint64(fst.Dev) //nolint:unconvert // uint32 on some architectures
	if isOn(fd, dev, zfsSuperMagic) || !allocates(fd) {
		return st, false
	}
	return st, hasHoles(size, fst.Blocks, func() (int64, error) {
		if genericSeekHole(fd, dev) {
			return 0, unix.EOPNOTSUPP // the blocks decide (S3)
		}
		return unix.Seek(fd, 0, seekHole)
	})
}

// HasHolesToFill tells whether FillHoles would fill holes in the regular file
// at path, of size bytes (fileHoles), for restore --dry-run.
func HasHolesToFill(path string, size int64) bool {
	_, holes := fileHoles(path, size)
	return holes
}

// FillHoles allocates the holes of the regular file at path, of size bytes,
// which the restorer leaves as it is (its contents are right) but should be
// fully allocated: a swap file left with holes by an interrupted restore.
// Preallocating a file's whole length fills its holes without touching its
// data.  The file is opened for writing only when it has holes
// (fileHoles): an unchanged file without them is never opened for writing,
// so gets no close-write event, and a running program's file no ETXTBSY.
// It returns whether the file got blocks.  A file system that can't
// preallocate (EOPNOTSUPP) is nothing to do.  Any other failure (ENOSPC) is
// returned, for the restorer to warn about, with whether the file got
// blocks all the same: it is looked at again after a failed fallocate,
// which may have allocated part of the holes.
func FillHoles(path string, size int64) (bool, error) {
	st, holes := fileHoles(path, size)
	if !holes {
		return false, nil
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil {
		return false, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	if before.Ino != st.Ino || before.Dev != st.Dev {
		debug.Log("not filling holes of %v: replaced", path)
		return false, nil
	}
	err = ignoringEINTR(func() error { return unix.Fallocate(fd, 0, 0, size) })
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
		debug.Log("not filling holes of %v: %v", path, err)
		return false, nil
	}
	var after unix.Stat_t
	grew := true
	if ferr := unix.Fstat(fd, &after); ferr == nil {
		grew = after.Blocks > before.Blocks
	} else if err != nil {
		grew = false
	}
	if err != nil {
		// ext4 and XFS keep what fallocate allocated before it failed
		// (ENOSPC): the file has changed if its blocks grew.
		return grew, &os.PathError{Op: "fallocate", Path: path, Err: err}
	}
	return grew, nil
}
