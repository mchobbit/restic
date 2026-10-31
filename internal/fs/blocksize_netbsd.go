package fs

import "golang.org/x/sys/unix"

// fsBlockSize returns the block size of the file system fd is on, or 0.
func fsBlockSize(fd int) int64 {
	var st unix.Statvfs_t
	if unix.Fstatvfs(fd, &st) != nil {
		return 0
	}
	return int64(st.Bsize)
}
