package restorer

import (
	"github.com/restic/restic/internal/restic"
)

// WriteAt writes p to f.File at offset.  A file restored sparse gets the
// parts of p that are all zeros within a file system block as holes
// (writeSparse).
func (f *partialFile) WriteAt(p []byte, offset int64) (n int, err error) {
	if !f.sparse {
		return f.File.WriteAt(p, offset)
	}
	return writeSparse(f.File.WriteAt, p, offset, f.blockSize)
}

// writeSparse writes p at offset with writeAt into a file restored sparse
// (BackOtter's S4), which is new and sized with ftruncate, so that it reads
// as zeros wherever nothing is written.  p is cut where the blocks of bs
// bytes, aligned to the file offset, begin: a piece of it in one block that
// is all zeros is left unwritten, and every other piece is written as it is,
// with nothing outside p, in as few writes as the zero pieces between them
// leave.  So a block gets written only where p puts a byte other than zero.
func writeSparse(writeAt func(p []byte, offset int64) (int, error), p []byte, offset int64, bs int64) (n int, err error) {
	end := offset + int64(len(p))
	// p from start on is still to be written or skipped
	start := offset
	for piece := offset; piece < end; {
		next := min((piece/bs+1)*bs, end)
		zeros := p[piece-offset : next-offset]
		if restic.ZeroPrefixLen(zeros) == len(zeros) {
			if piece > start {
				n2, err := writeAt(p[start-offset:piece-offset], start)
				if err != nil {
					return int(start-offset) + n2, err
				}
			}
			start = next
		}
		piece = next
	}
	if end > start {
		n2, err := writeAt(p[start-offset:], start)
		if err != nil {
			return int(start-offset) + n2, err
		}
	}
	return len(p), nil
}
