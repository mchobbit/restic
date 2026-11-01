//go:build linux || freebsd || darwin || illumos

package fs

import (
	"errors"
	"io"
	"math"
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// SkipHoles returns the reader to back up the contents of f, which node
// describes.  A regular file marked as having holes (unix.sparse, which the
// SEEK_HOLE probe set on this OS) is read by its data extents: its holes are
// given as zeros without being read.  Everything else is f itself.  Either
// way the bytes are those that reading f gives, so the chunks are the same.
func SkipHoles(f File, node *data.Node) io.Reader {
	inner := f
	if tf, ok := f.(*trackFile); ok {
		inner = tf.File
	}
	lf, ok := inner.(*localFile)
	if !ok || lf.f == nil || node.Type != data.NodeTypeFile {
		return f
	}
	if sparse, err := node.Sparse(); err != nil || !sparse {
		return f
	}
	return &holeReader{f: osExtents{lf.f}, name: lf.name}
}

// extentFile is what holeReader reads: an *os.File (osExtents), or a
// simulated file in tests.
type extentFile interface {
	ReadAt(p []byte, off int64) (int, error)
	// Seek is lseek(2), with seekData and seekHole.
	Seek(offset int64, whence int) (int64, error)
	// Size is fstat(2)'s size.
	Size() (int64, error)
}

// osExtents is the extentFile of an open file.
type osExtents struct {
	*os.File
}

func (f osExtents) Size() (int64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// holeReader reads a file by its data extents.  Up to end, the file is a
// hole (zeros, not read) or data (read with pread(2)); at end, lseek(2) with
// SEEK_DATA and SEEK_HOLE finds the next extent.  Once that fails, or the
// file has no data left but its trailing hole, the rest is read as data, as
// plain reading does: up to whatever end of file pread finds, so that a file
// that grows or shrinks while it is read gives no more trouble than it does
// to plain reading.  A hole ends at the file's size when each buffer of its
// zeros is given, so that a file that shrank meanwhile ends where plain
// reading ends it.
type holeReader struct {
	f    extentFile
	name string
	off  int64 // the offset of the next byte to give
	end  int64 // where the extent off is in ends
	hole bool  // whether that extent is a hole

	holeBytes int64 // bytes given as zeros without reading them (for tests)
}

// plain is the extent of all the rest of the file, read as data.
const plain = math.MaxInt64

func (r *holeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.off >= r.end {
		r.next()
	}
	if r.hole {
		// The file may have shrunk since the hole was found: the hole
		// ends at its size now, and from there on the file is read
		// plainly (to its end of file, which pread finds).
		if size, err := r.f.Size(); err == nil && size < r.end {
			r.end = size
			if r.off >= size {
				r.hole, r.end = false, plain
			}
		}
	}
	if int64(len(p)) > r.end-r.off {
		p = p[:r.end-r.off]
	}
	if r.hole {
		clear(p)
		r.off += int64(len(p))
		r.holeBytes += int64(len(p))
		return len(p), nil
	}
	n, err := r.f.ReadAt(p, r.off)
	r.off += int64(n)
	if err == io.EOF && n > 0 {
		// The end of file is given by the next Read, as read(2) does.
		err = nil
	}
	return n, err
}

// next finds the extent that starts at r.off.
func (r *holeReader) next() {
	// The size before SEEK_DATA, so that data appended after it isn't taken
	// for a hole.
	size, err := r.f.Size()
	if err != nil {
		size = -1
	}
	start, err := r.f.Seek(r.off, seekData)
	switch {
	case errors.Is(err, unix.ENXIO):
		// No data from r.off on: a hole up to the end of file (the size
		// before SEEK_DATA, or after it if smaller), or r.off is at the end.
		// A hole only where SEEK_HOLE finds one at r.off too: macOS answers
		// ENXIO to both at every offset of a file its file system
		// compresses, whose data plain reading gives.
		r.hole, r.end = false, plain
		if now, err := r.f.Size(); err == nil && min(size, now) > r.off {
			if hole, err := r.f.Seek(r.off, seekHole); err == nil && hole == r.off {
				r.hole, r.end = true, min(size, now)
			}
		}
	case err != nil:
		debug.Log("no SEEK_DATA for %v at %d: %v", r.name, r.off, err)
		r.hole, r.end = false, plain
	case start > r.off:
		// A hole up to the data, but not past the size before SEEK_DATA
		// (which a correct file system never puts data past).
		r.hole, r.end = false, plain
		if min(start, size) > r.off {
			r.hole, r.end = true, min(start, size)
		}
	default:
		hole, err := r.f.Seek(r.off, seekHole)
		if err != nil || hole <= r.off {
			debug.Log("no SEEK_HOLE for %v at %d: %v", r.name, r.off, err)
			hole = plain
		}
		r.hole, r.end = false, hole
	}
}
