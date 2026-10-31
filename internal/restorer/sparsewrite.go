package restorer

import (
	"github.com/restic/restic/internal/restic"
)

// WriteAt writes p to f.File at offset.  A file restored sparse gets the
// blocks of zeros in p as holes (writeSparse).
func (f *partialFile) WriteAt(p []byte, offset int64) (n int, err error) {
	if !f.sparse {
		return f.File.WriteAt(p, offset)
	}
	return writeSparse(f.File.WriteAt, p, offset, f.blockSize)
}

// writeSparse writes p at offset with writeAt into a file restored sparse
// (BackOtter's S4), which is new and sized with ftruncate, so that it reads
// as zeros wherever nothing is written: of p, every block of bs bytes,
// aligned to the file offset, that lies wholly inside p and is all zeros is
// left unwritten, as a hole, and the rest, partial blocks at p's ends
// included, is written, in as few writes as the zero blocks between leave.
func writeSparse(writeAt func(p []byte, offset int64) (int, error), p []byte, offset int64, bs int64) (n int, err error) {
	end := offset + int64(len(p))
	// p from start on is still to be written or skipped
	start := offset
	for block := (offset + bs - 1) / bs * bs; block+bs <= end; block += bs {
		zeros := p[block-offset : block+bs-offset]
		if restic.ZeroPrefixLen(zeros) != len(zeros) {
			continue
		}
		if block > start {
			n2, err := writeAt(p[start-offset:block-offset], start)
			if err != nil {
				return int(start-offset) + n2, err
			}
		}
		start = block + bs
	}
	if end > start {
		n2, err := writeAt(p[start-offset:], start)
		if err != nil {
			return int(start-offset) + n2, err
		}
	}
	return len(p), nil
}
