package fs

import (
	"fmt"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
)

// File flags (FreeBSD's chflags(1) flags, Linux's chattr(1) flags) are kept in
// a generic attribute per OS, fileFlagsAttribute.  They are restored last,
// after the restorer has finished with every file: an immutable file refuses
// new hard links, timestamps and permissions, and an immutable directory new
// entries.

// errNotNodeType is the error for a file opened for its flags or holes that
// isn't the one expected: of another type, or, at backup, another inode than
// the one lstat(2) saw (replaced in between).
var errNotNodeType = errors.New("not the file expected (replaced?)")

// NodeFileFlags returns the file flags node holds for this OS, and whether it
// holds any.  Malformed flags are none, with a data.MalformedAttributeError
// for the restorer to warn about.
func NodeFileFlags(node *data.Node) (uint32, bool, error) {
	if fileFlagsAttribute == "" {
		return 0, false, nil
	}
	return node.FileFlags(fileFlagsAttribute)
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

// HasDirFlagsBeforeEntries tells whether this OS has flags that a file system
// only takes while a directory is empty, for PrepareDirForFlags to set.
func HasDirFlagsBeforeEntries() bool {
	return dirFlagsBeforeEntries != 0
}

// PrepareDirForFlags makes those flags of the empty directory at path that a
// file system only takes while a directory is empty (casefolding on ext4 and
// f2fs) what flags says, before anything goes in: it sets them, or clears
// them where the directory inherited them from its parent.  The restorer
// calls it for every directory it creates, and every existing one it finds
// empty, whatever their nodes hold.  A file system without such flags is
// left alone, silently.
func PrepareDirForFlags(path string, flags uint32) error {
	if dirFlagsBeforeEntries == 0 {
		return nil
	}
	return prepareDirForFlags(path, flags&dirFlagsBeforeEntries)
}

// RestoreFileFlags sets the file flags of the file at path, which has type
// typ, to flags.  Flags the file system doesn't support are left out, and
// reported in the error, an *UnsupportedError when that is all there is to
// report.  compression is the file's btrfs.compression extended attribute,
// if it has one and the restore sets it: on btrfs, setting flags rewrites
// it, so it is set again (Linux).
func RestoreFileFlags(path string, typ data.NodeType, flags uint32, compression []byte) error {
	return restoreFileFlags(path, typ, flags, compression)
}

// setFlagsBestEffort calls set with want.  If that fails, it sets as many of
// want's bits as set accepts, one at a time, and returns an error naming the
// bits it refused.  When every refusal says the file system doesn't support
// a bit (unsupportedErr), that is an *UnsupportedError wrapping set's first
// error; otherwise the error wraps the first other refusal.
func setFlagsBestEffort(want uint32, set func(uint32) error) error {
	firstErr := set(want)
	if firstErr == nil {
		return nil
	}
	var got, refused uint32
	var failed error
	for bit := uint32(1); bit != 0; bit <<= 1 {
		if want&bit == 0 {
			continue
		}
		err := set(got | bit)
		switch {
		case err == nil:
			got |= bit
		case unsupportedErr(err):
			refused |= bit
		default:
			refused |= bit
			if failed == nil {
				failed = err
			}
		}
	}
	if refused == 0 {
		return nil
	}
	if failed == nil {
		return &UnsupportedError{Attr: fileFlagsAttribute, Bits: refused, Err: firstErr}
	}
	return fmt.Errorf("file flags %#x of %#x not set: %w", refused, want, failed)
}
