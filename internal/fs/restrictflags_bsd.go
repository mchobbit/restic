//go:build freebsd || netbsd || openbsd || dragonfly

package fs

import (
	"errors"
	"os"

	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// ClearRestrictingFlags clears the flags of the file at path (not following
// a symlink) that would keep a restore from rewriting, replacing or
// changing it, before the restorer touches a file it finds in its way:
// immutable, append-only and undeletable (schg, uchg, sappnd, uappnd, and
// sunlnk, uunlnk where the system has them), and on FreeBSD ZFS's DOS
// read-only (urdonly).  The others stay as they are; the late flags pass
// gives the file its node's.  Nothing there, or no such flags, is nothing
// to do.  A failure (a securelevel above 0, not root) is only logged: what
// the restorer does next to the file fails and says why.  As in
// rustic-bko.
func ClearRestrictingFlags(path string) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		if !errors.Is(err, unix.ENOENT) {
			debug.Log("restricting flags of %v: %v", path, err)
		}
		return
	}
	flags := uint32(st.Flags)
	if flags&restrictingFlags == 0 {
		return
	}
	var err error
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		err = lchflags(path, flags&^restrictingFlags)
	} else if err = unix.Chflags(path, int(flags&^restrictingFlags)); err != nil {
		err = &os.PathError{Op: "chflags", Path: path, Err: err}
	}
	if err != nil {
		debug.Log("restricting flags of %v not cleared: %v", path, err)
	}
}
