//go:build openbsd && (amd64 || arm64)

package fs

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// lchflags sets a symlink's own flags: chflagsat(2) with
// AT_SYMLINK_NOFOLLOW.  OpenBSD takes system calls only through libc, and
// x/sys/unix has no chflagsat, so it's reached the way x/sys/unix reaches
// libc there: a trampoline (lchflags_openbsd_*.s) and the runtime's
// syscall6.
func lchflags(path string, flags uint32) error {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	dirfd := unix.AT_FDCWD // negative, so not a constant uintptr
	_, _, errno := syscall_syscall6(libc_chflagsat_trampoline_addr,
		uintptr(dirfd), uintptr(unsafe.Pointer(p)), uintptr(flags), uintptr(unix.AT_SYMLINK_NOFOLLOW), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

//go:cgo_import_dynamic libc_chflagsat chflagsat "libc.so"

var libc_chflagsat_trampoline_addr uintptr

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)
