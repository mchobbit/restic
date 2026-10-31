//go:build !windows

package fs

import (
	"os"

	"golang.org/x/sys/unix"
)

// HoleBlockSize returns the size of the blocks to leave as holes in f, a
// file being restored sparse (BackOtter): its file system's block size
// (statfs(2)'s or statvfs(2)'s f_bsize), else f's st_blksize, at least
// 4096; 4096 if neither is known.
func HoleBlockSize(f *os.File) int64 {
	var bs int64
	rc, err := f.SyscallConn()
	if err == nil {
		_ = rc.Control(func(fd uintptr) {
			bs = fsBlockSize(int(fd))
			if bs <= 0 {
				var st unix.Stat_t
				if unix.Fstat(int(fd), &st) == nil {
					bs = int64(st.Blksize)
				}
			}
		})
	}
	return max(bs, minHoleBlockSize)
}
