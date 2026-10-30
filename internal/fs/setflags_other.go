//go:build !linux

package fs

import "os"

// OS-specific replacements of setFlags can set file status flags
// that improve I/O performance.
func setFlags(_ *os.File) error {
	return nil
}

// SetNoatime does nothing here: only Linux has O_NOATIME.
func SetNoatime(_ *os.File) {}
