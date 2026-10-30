//go:build !windows

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
			hole, err := seekFirstHole(path)
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

// seekFirstHole returns the offset of the first hole in the file at path, or
// its size if it has none.
func seekFirstHole(path string) (int64, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, err
	}
	defer func() { _ = unix.Close(fd) }()
	return unix.Seek(fd, 0, seekHole)
}
