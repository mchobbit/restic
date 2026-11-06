package fs

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func PreallocateFile(wr *os.File, size int64) error {
	if size <= 0 {
		return nil
	}
	fd := int(wr.Fd())
	if !allocates(fd) {
		// BackOtter: sized as fallocate(2) would, without allocating.
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil || st.Size >= size {
			return err
		}
		return unix.Ftruncate(fd, size)
	}
	// int fallocate(int fd, int mode, off_t offset, off_t len)
	// use mode = 0 to also change the file size
	return ignoringEINTR(func() error { return unix.Fallocate(fd, 0, 0, size) })
}

// btrfsSuperMagic is BTRFS_SUPER_MAGIC, statfs(2)'s f_type on btrfs.
const btrfsSuperMagic = 0x9123683e

// fsNocowFl is FS_NOCOW_FL, btrfs's no-copy-on-write file flag (C).
const fsNocowFl = 0x00800000

// allocates tells whether fallocate(2) is to allocate the file open as fd
// (preallocate it, or fill its holes), by BackOtter's rule S5: not on btrfs
// but for a no-copy-on-write file (FS_NOCOW_FL, a swap file), and never on
// bcachefs.  Neither compresses what fallocate(2) allocated, so a restore
// onto a compressing mount (compress=zstd) would need every file's full
// size, and might not fit where the source did.  A NOCOW file isn't
// compressed anyway, and a swap file must have no holes.  bcachefs's
// no-copy-on-write is an inode option, not a file flag, and it has no swap
// files.  Where the file system can't be told, it is allocated.
func allocates(fd int) bool {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return true
	}
	return allocatesType(uint32(st.Type), func() bool {
		flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
		return err == nil && flags&fsNocowFl != 0
	})
}

// allocatesType is allocates for the file system whose statfs(2) f_type is
// typ, nocow telling whether the file has FS_NOCOW_FL.
func allocatesType(typ uint32, nocow func() bool) bool {
	switch typ {
	case btrfsSuperMagic:
		return nocow()
	case bcachefsSuperMagic:
		return false
	}
	return true
}

// ignoringEINTR makes a function call and repeats it if it returns
// an EINTR error.
// copied from /usr/lib/go/src/internal/poll/fd_posix.go of go 1.23.1
func ignoringEINTR(fn func() error) error {
	for {
		err := fn()
		if err != syscall.EINTR {
			return err
		}
	}
}
