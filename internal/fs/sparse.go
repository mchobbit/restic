package fs

// hasHoles tells whether a regular file of size bytes, with blocks 512-byte
// blocks allocated, has holes.  Fewer blocks than its size needs only say it
// may have: a file on a compressed file system has few blocks and no holes.
// firstHole, where the OS has SEEK_HOLE, returns the offset of the file's
// first hole (its size if it has none), and is only called when the blocks
// leave room for one.  The blocks guard against SEEK_HOLE in turn: a
// preallocated file (a swap file) has unwritten extents, which SEEK_HOLE may
// report as holes but which count as blocks.
//
//nolint:unused // not used on Windows
func hasHoles(size, blocks int64, firstHole func() (int64, error)) bool {
	if size <= 0 || blocks*512 >= size {
		return false
	}
	if firstHole == nil {
		return true
	}
	hole, err := firstHole()
	return err == nil && hole < size
}
