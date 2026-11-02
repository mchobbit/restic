//go:build illumos

package fs

import "syscall"

// sIFDOOR is S_IFDOOR, the file type of an illumos door (sys/stat.h).
const sIFDOOR = 0xd000

// IsDoor tells whether fi is an illumos door: a running program's door
// server, made again when it starts (/etc/sysevent, /var/run), whose
// restored copy has nobody behind it.  BackOtter skips doors, as it skips
// sockets, quietly, in rustic-bko too; stock restic fails on each one
// (unsupported file type "irregular") and exits 3.
func IsDoor(fi *ExtendedFileInfo) bool {
	s, ok := fi.sys.(*syscall.Stat_t)
	return ok && s.Mode&syscall.S_IFMT == sIFDOOR
}
