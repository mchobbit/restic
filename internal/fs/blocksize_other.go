//go:build !linux && !darwin && !freebsd && !dragonfly && !openbsd && !netbsd && !solaris && !windows

package fs

// fsBlockSize returns 0: the file system's block size isn't known here.
func fsBlockSize(_ int) int64 {
	return 0
}
