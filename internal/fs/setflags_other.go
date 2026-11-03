//go:build !linux

package fs

import (
	"os"
	"runtime"
)

// ListingKeepsAtimes tells whether listing a directory can leave its access
// time as it was.  Only Linux has O_NOATIME: on every other Unix a listing
// moves it, unless the file system keeps no access times (read-only,
// noatime, ZFS's atime=off), so a backup that stores real access times
// (--with-atime) doesn't run the scanner there (as with --no-scan), as
// rustic-bko doesn't with --set-atime yes: it would list each directory
// before the archiver takes its lstat, and the snapshot would have the
// scanner's time as the access time.  Without --with-atime the scanner runs,
// as in stock restic.  Windows: as stock restic.
const ListingKeepsAtimes = runtime.GOOS == "windows"

// OS-specific replacements of setFlags can set file status flags
// that improve I/O performance.
func setFlags(_ *os.File) error {
	return nil
}

// SetNoatime does nothing here: only Linux has O_NOATIME.
func SetNoatime(_ *os.File) {}
