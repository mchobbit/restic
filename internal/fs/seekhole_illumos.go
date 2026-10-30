//go:build illumos

package fs

// SEEK_HOLE from illumos' <sys/unistd.h>, which x/sys/unix lacks.
const seekHole = 4
