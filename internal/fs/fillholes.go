//go:build linux || darwin

package fs

import (
	"os"

	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// FillHoles allocates the holes of the regular file at path, of size bytes,
// which the restorer leaves as it is (its contents are right) but should be
// fully allocated: a swap file left with holes by an interrupted restore.
// Preallocating a file's whole length fills its holes without touching its
// data.  It does nothing to a file without holes, and errors are only
// logged: the contents are right either way.
func FillHoles(path string, size int64) {
	f, err := os.OpenFile(path, unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		debug.Log("not filling holes of %v: %v", path, err)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		debug.Log("not filling holes of %v: %v", path, err)
		return
	}
	if efi := ExtendedStat(fi); efi.Blocks*512 >= size {
		return
	}
	if err := PreallocateFile(f, size); err != nil {
		debug.Log("failed to fill holes of %v: %v", path, err)
	}
}
