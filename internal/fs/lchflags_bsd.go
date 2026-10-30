//go:build freebsd || netbsd || dragonfly

package fs

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// lchflags is chflags(2) without following a symlink, which x/sys/unix lacks.
func lchflags(path string, flags uint32) error {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall(unix.SYS_LCHFLAGS, uintptr(unsafe.Pointer(p)), uintptr(flags), 0)
	if errno != 0 {
		return &os.PathError{Op: "lchflags", Path: path, Err: errno}
	}
	return nil
}
