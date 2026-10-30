//go:build openbsd && !amd64 && !arm64

package fs

import "golang.org/x/sys/unix"

// lchflags has no libc trampoline on this architecture (lchflags_openbsd.go):
// a symlink's own flags aren't restored.
func lchflags(_ string, _ uint32) error {
	return unix.ENOTSUP
}
