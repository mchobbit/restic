package fs

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneContents gives dst src's contents, sharing its extents (FICLONE):
// btrfs, XFS with reflinks, bcachefs, OCFS2.  Elsewhere it fails
// (EOPNOTSUPP, EXDEV across file systems, EINVAL for btrfs's
// no-copy-on-write files).
func cloneContents(dst, src *os.File) error {
	if err := unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())); err != nil {
		return &os.PathError{Op: "FICLONE", Path: dst.Name(), Err: err}
	}
	return nil
}
