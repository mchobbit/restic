package fs

// minHoleBlockSize is the smallest block a restore leaves as a hole: a
// sector, the block of bcachefs formatted with block_size 512, the
// smallest any file system has.  A file system's own smaller blocks (XFS,
// ext2/3/4 with 1 KiB blocks, bcachefs) get holes as small as its source's.
const minHoleBlockSize = 512

// defaultHoleBlockSize is the block a restore leaves as a hole where the
// file system's block size isn't known (and on Windows).
const defaultHoleBlockSize = 4096

// holeBlockSize is the block a restore leaves as a hole on a file system
// whose block size is bs (0 or less: not known): bs, at least
// minHoleBlockSize; defaultHoleBlockSize where it isn't known.
func holeBlockSize(bs int64) int64 {
	if bs <= 0 {
		return defaultHoleBlockSize
	}
	return max(bs, minHoleBlockSize)
}
