//go:build illumos

package fs

// SEEK_DATA and SEEK_HOLE from illumos' <sys/unistd.h>, which x/sys/unix
// lacks.
const (
	seekData = 3
	seekHole = 4
)
