//go:build !linux

package fs

// Only Linux has flags that must be set before a file's data or a
// directory's entries (flags_linux.go).
const (
	flagsBeforeData       = 0
	dirFlagsBeforeEntries = 0
)

func prepareFileForFlags(_ string, _ uint32) error {
	return nil
}

func prepareDirForFlags(_ string, _ uint32) error {
	return nil
}
