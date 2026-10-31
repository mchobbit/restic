//go:build linux || freebsd || darwin

package fs

import "golang.org/x/sys/unix"

// lseek(2)'s whence for the next hole (sparse_unix.go) and the next data
// (holes.go).
const (
	seekHole = unix.SEEK_HOLE
	seekData = unix.SEEK_DATA
)
