package fs

// minHoleBlockSize is the smallest block a restore leaves as a hole, and the
// one it uses when the file system's block size isn't known.
const minHoleBlockSize = 4096
