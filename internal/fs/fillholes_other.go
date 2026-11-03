//go:build !linux

package fs

// HasHolesToFill is always false here: FillHoles fills nothing.
func HasHolesToFill(_ string, _ int64) bool { return false }

// FillHoles does nothing here.  Other systems have no call that fills a
// file's holes without touching its data: macOS's F_PREALLOCATE with
// F_PEOFPOSMODE only adds blocks past the end of file, not in the holes
// inside it, and elsewhere PreallocateFile only sets the size.  A file
// whose contents are right keeps its holes, as in stock restic.
func FillHoles(_ string, _ int64) (bool, error) { return false, nil }
