//go:build !linux

package fs

import (
	"os"
	"runtime"
)

// ListingKeepsAtimes tells whether listing a directory can leave its access
// time as it was.  Only Linux has O_NOATIME: on every other Unix a listing
// moves it, unless the file system keeps no access times (read-only,
// noatime, ZFS's atime=off), so a backup doesn't run the scanner there
// (--no-scan), as rustic-bko doesn't: it would list each directory before
// the archiver takes its lstat, and the snapshot would have the scanner's
// time as the access time.  Windows: as stock restic.
const ListingKeepsAtimes = runtime.GOOS == "windows"

// OS-specific replacements of setFlags can set file status flags
// that improve I/O performance.
func setFlags(_ *os.File) error {
	return nil
}

// SetNoatime does nothing here: only Linux has O_NOATIME.
func SetNoatime(_ *os.File) {}
