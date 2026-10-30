package fs

import (
	"fmt"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
)

// File flags (FreeBSD's chflags(1) flags, Linux's chattr(1) flags) are kept in
// a generic attribute per OS, fileFlagsAttribute.  They are restored last,
// after the restorer has finished with every file: an immutable file refuses
// new hard links, timestamps and permissions, and an immutable directory new
// entries.

// NodeFileFlags returns the file flags node holds for this OS, and whether it
// holds any.
func NodeFileFlags(node *data.Node) (uint32, bool) {
	if fileFlagsAttribute == "" {
		return 0, false
	}
	flags, ok, err := node.FileFlags(fileFlagsAttribute)
	if err != nil {
		debug.Log("ignoring file flags of %v: %v", node.Name, err)
		return 0, false
	}
	return flags, ok
}

// PrepareFileForFlags creates an empty file at path with those of flags that
// a file system only takes while a file is empty (btrfs's no-copy-on-write),
// for the restorer to write the contents into.  It does nothing when there
// are none, or when path exists already.
func PrepareFileForFlags(path string, flags uint32) error {
	if flags&flagsBeforeData == 0 {
		return nil
	}
	return prepareFileForFlags(path, flags&flagsBeforeData)
}

// RestoreFileFlags sets the file flags of the file at path, which has type
// typ, to flags.  Flags the file system doesn't support are left out, and
// reported in the error.
func RestoreFileFlags(path string, typ data.NodeType, flags uint32) error {
	return restoreFileFlags(path, typ, flags)
}

// setFlagsBestEffort calls set with want.  If that fails, it sets as many of
// want's bits as set accepts, one at a time, and returns an error naming the
// bits it refused, wrapping set's first error.
func setFlagsBestEffort(want uint32, set func(uint32) error) error {
	firstErr := set(want)
	if firstErr == nil {
		return nil
	}
	var got, refused uint32
	for bit := uint32(1); bit != 0; bit <<= 1 {
		if want&bit == 0 {
			continue
		}
		if set(got|bit) == nil {
			got |= bit
		} else {
			refused |= bit
		}
	}
	if refused == 0 {
		return nil
	}
	return fmt.Errorf("file flags %#x of %#x not set: %w", refused, want, firstErr)
}
