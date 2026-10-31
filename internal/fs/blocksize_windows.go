package fs

import "os"

// HoleBlockSize returns the size of the blocks to leave as holes in f, a
// file being restored sparse (BackOtter): 4096, the file system's block
// size not being looked up on Windows.
func HoleBlockSize(_ *os.File) int64 {
	return minHoleBlockSize
}
