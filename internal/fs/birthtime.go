package fs

import (
	"time"

	"github.com/restic/restic/internal/data"
)

// Birth times (unix.birthtime) are kept on FreeBSD, NetBSD and illumos.
// illumos sets one like any other system attribute (birthtime_illumos.go).
// FreeBSD and NetBSD have no call that sets a birth time, but their kernels
// lower it to any mtime set earlier than it, on UFS2, FFSv2 and ZFS alike,
// for files, directories and symlinks, and for their owners too.  So a
// birth time earlier than the mtime is set as the mtime first, leaving the
// atime as it is, and the final atime and mtime then leave it
// (birthtime_bsd.go).  One that is not earlier needs nothing: the final
// times lower the new file's birth time, the time of the restore, to the
// mtime, the closest they can get.

// birthTimeFirst returns the mtime to set before node's final atime and
// mtime on FreeBSD and NetBSD, for its birth time to come back, and whether
// there is one to set: its birth time, when that is earlier than its mtime.
//
//nolint:unused // used on FreeBSD and NetBSD
func birthTimeFirst(node *data.Node) (time.Time, bool, error) {
	birth, ok, err := node.BirthTime()
	if !ok || err != nil || !birth.Before(node.ModTime) {
		return time.Time{}, false, err
	}
	return birth, true, nil
}
