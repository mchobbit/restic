//go:build !windows && !linux

package fs

import (
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// nodeFillSparse marks a regular file that has holes (data.TypeUnixSparse),
// for the restorer to write it sparse again; see hasHoles.
func nodeFillSparse(node *data.Node, path string, fi *ExtendedFileInfo) {
	if node.Type != data.NodeTypeFile {
		return
	}
	var firstHole func() (int64, error)
	if seekHole >= 0 {
		firstHole = func() (int64, error) {
			hole, err := seekFirstHole(path, fi)
			if err != nil {
				debug.Log("no SEEK_HOLE for %v: %v", path, err)
			}
			return hole, err
		}
	}
	if hasHoles(fi.Size, fi.Blocks, firstHole) {
		node.SetSparse()
	}
}

// seekFirstHole returns the offset of the first hole in the regular file at
// path, which fi describes, or its size if it has none.  A file that isn't
// the one fi describes any more gives errNotNodeType.
func seekFirstHole(path string, fi *ExtendedFileInfo) (int64, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || uint64(st.Dev) != fi.DeviceID || uint64(st.Ino) != fi.Inode {
		return 0, errNotNodeType
	}
	return unix.Seek(fd, 0, seekHole)
}
