package fs

import "github.com/restic/restic/internal/data"

// On Linux, nodeFillFileFlags marks a file with holes, through the descriptor
// it opens for the flags and project ID anyway.
func nodeFillSparse(_ *data.Node, _ string, _ *ExtendedFileInfo) {}

// statfs(2)'s f_type on the file systems whose SEEK_HOLE is the kernel's
// generic one, which finds no hole before the size (Linux 6.12): JFS and
// nilfs2.  Neither compresses nor keeps data in the inode, so their blocks
// tell holes apart (S3).
const (
	jfsSuperMagic   = 0x3153464a // JFS_SUPER_MAGIC
	nilfsSuperMagic = 0x3434     // NILFS_SUPER_MAGIC
)

// genericSeekHole tells whether the file open as fd, on device dev, is on a
// file system whose SEEK_HOLE can't find holes (jfsSuperMagic,
// nilfsSuperMagic).
func genericSeekHole(fd int, dev uint64) bool {
	return isOn(fd, dev, jfsSuperMagic) || isOn(fd, dev, nilfsSuperMagic)
}
