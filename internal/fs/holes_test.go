//go:build linux || freebsd || darwin || illumos

package fs

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// extent is a range of a test file that is written.  Zero data is written
// too: it is allocated, not a hole.
type extent struct {
	off  int64
	data []byte
}

// sparseLayout is a file of size bytes with only its extents written.
type sparseLayout struct {
	name    string
	size    int64
	extents []extent
}

func (l sparseLayout) create(t testing.TB, dir string) string {
	path := filepath.Join(dir, l.name)
	f, err := os.Create(path)
	rtest.OK(t, err)
	for _, e := range l.extents {
		_, err := f.WriteAt(e.data, e.off)
		rtest.OK(t, err)
	}
	rtest.OK(t, f.Truncate(l.size))
	rtest.OK(t, f.Close())
	return path
}

func randomBytes(rnd *rand.Rand, n int) []byte {
	buf := make([]byte, n)
	_, _ = rnd.Read(buf)
	if rnd.Intn(8) == 0 {
		// data that is zeros, but allocated
		clear(buf)
	} else if rnd.Intn(4) == 0 {
		// data with zero runs inside
		for i := 0; i < 4; i++ {
			start := rnd.Intn(n)
			clear(buf[start:min(n, start+rnd.Intn(16384))])
		}
	}
	return buf
}

// holeLayouts are fixed layouts and seeded random ones: holes at the start,
// in the middle and at the end, data at the end of file, a whole-file hole,
// holes shorter than a page.
func holeLayouts(rnd *rand.Rand, random int) []sparseLayout {
	const mib = 1 << 20
	layouts := []sparseLayout{
		{"empty", 0, nil},
		{"all hole", 9 * mib, nil},
		{"all hole, small", 100, nil},
		{"hole at start", 3 * mib, []extent{{2 * mib, randomBytes(rnd, mib)}}},
		{"hole at end", 5 * mib, []extent{{0, randomBytes(rnd, 100000)}}},
		{"hole in the middle", 4*mib + 3, []extent{{0, randomBytes(rnd, 70000)}, {4 * mib, []byte("end")}}},
		{"data at EOF", 6*mib + 1, []extent{{6 * mib, []byte{1}}}},
		{"hole shorter than a page", 3 * mib, []extent{{mib, randomBytes(rnd, 5000)}, {mib + 6000, randomBytes(rnd, 5000)}}},
		{"one byte per 64 KiB", 4 * mib, func() (es []extent) {
			for off := int64(0); off < 4*mib; off += 64 << 10 {
				es = append(es, extent{off, []byte{0xff}})
			}
			return es
		}()},
		{"dense", 2*mib + 17, []extent{{0, randomBytes(rnd, 2*mib+17)}}},
	}
	for i := 0; i < random; i++ {
		size := rnd.Int63n(24 * mib)
		var es []extent
		for j := rnd.Intn(12); j > 0; j-- {
			n := 1 + rnd.Intn([]int{10, 5000, 70000, 600000, 3 * mib}[rnd.Intn(5)])
			off := rnd.Int63n(size + 1)
			if rnd.Intn(4) == 0 {
				// data at, or past, the end of file
				off = max(0, size-int64(n)+rnd.Int63n(2))
			}
			es = append(es, extent{off, randomBytes(rnd, n)})
		}
		size = max(size, rnd.Int63n(size+1))
		layouts = append(layouts, sparseLayout{"random", size, es})
	}
	return layouts
}

// readAllBy reads r to its end, with reads of random sizes.
func readAllBy(t testing.TB, r io.Reader, rnd *rand.Rand) []byte {
	var out []byte
	for {
		buf := make([]byte, 1+rnd.Intn([]int{1, 4096, 100000, chunker.MaxSize}[rnd.Intn(4)]))
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out
		}
		rtest.OK(t, err)
	}
}

type chunkSum struct {
	length uint
	id     [32]byte
}

func chunksOf(t testing.TB, r io.Reader, pol chunker.Pol) (chunks []chunkSum) {
	chnker := chunker.New(r, pol)
	buf := make([]byte, chunker.MaxSize)
	for {
		c, err := chnker.Next(buf)
		if err == io.EOF {
			return chunks
		}
		rtest.OK(t, err)
		chunks = append(chunks, chunkSum{c.Length, sha256.Sum256(c.Data)})
	}
}

func openSparse(t testing.TB, path string) (File, *data.Node) {
	f, err := Local{}.OpenFile(path, O_RDONLY, false)
	rtest.OK(t, err)
	t.Cleanup(func() { _ = f.Close() })
	node := &data.Node{Type: data.NodeTypeFile}
	node.SetSparse()
	return f, node
}

// Reading by extents gives exactly the bytes, and so the chunks, that
// reading the file plainly gives, and leaves its holes unread.
func TestSkipHolesSameBytes(t *testing.T) {
	dir := rtest.TempDir(t)
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	seed := rnd.Int63()
	t.Logf("seed %d", seed)
	rnd = rand.New(rand.NewSource(seed))
	pol, err := chunker.RandomPolynomial()
	rtest.OK(t, err)

	var skipped int64
	for i, layout := range holeLayouts(rnd, 40) {
		path := layout.create(t, dir)
		plain, err := os.ReadFile(path)
		rtest.OK(t, err)
		rtest.Equals(t, layout.size, int64(len(plain)))

		f, node := openSparse(t, path)
		r := SkipHoles(f, node)
		got := readAllBy(t, r, rnd)
		rtest.Assert(t, bytes.Equal(plain, got), "layout %d (%v): %d bytes read by extents differ from the %d read plainly", i, layout.name, len(got), len(plain))
		hr, ok := r.(*holeReader)
		rtest.Assert(t, ok, "a file marked sparse isn't read by its extents")
		skipped += hr.holeBytes

		plainFile, err := os.Open(path)
		rtest.OK(t, err)
		want := chunksOf(t, plainFile, pol)
		_ = plainFile.Close()
		f, node = openSparse(t, path)
		rtest.Equals(t, want, chunksOf(t, SkipHoles(f, node), pol))

		rtest.OK(t, os.Remove(path))
	}
	// A file system with holes (not every one has them) has the whole-file
	// hole at least.
	t.Logf("%d bytes of holes not read", skipped)
	probe := sparseLayout{"probe", 1 << 20, nil}.create(t, dir)
	if fi, err := os.Stat(probe); err == nil && ExtendedStat(fi).Blocks == 0 {
		rtest.Assert(t, skipped > 0, "no hole was skipped")
	}
}

// A file not marked sparse, or not a local file, is read as it is.
func TestSkipHolesOnlyMarked(t *testing.T) {
	dir := rtest.TempDir(t)
	path := sparseLayout{"holes", 1 << 20, []extent{{4096, []byte("x")}}}.create(t, dir)
	f, err := Local{}.OpenFile(path, O_RDONLY, false)
	rtest.OK(t, err)
	defer func() { _ = f.Close() }()

	node := &data.Node{Type: data.NodeTypeFile}
	rtest.Assert(t, SkipHoles(f, node) == io.Reader(f), "a file not marked sparse is read by its extents")
	node.SetSparse()
	_, ok := SkipHoles(f, node).(*holeReader)
	rtest.Assert(t, ok, "a file marked sparse isn't read by its extents")
	ff := fakeFile{name: "fake"}
	rtest.Assert(t, SkipHoles(ff, node) == io.Reader(ff), "a fake file is read by its extents")
}

// A file that grows or shrinks while it is read gives what plain reading
// gives: the bytes read before the change, then the file as it is from
// there to its end now, holes found before included.
func TestSkipHolesChangingFile(t *testing.T) {
	const mib = 1 << 20
	dir := rtest.TempDir(t)
	rnd := rand.New(rand.NewSource(1))
	layout := sparseLayout{"changing", 3 * mib, []extent{
		{0, randomBytes(rnd, 64<<10)},
		{mib, randomBytes(rnd, 64<<10)},
		{3*mib - 10, randomBytes(rnd, 10)},
	}}

	readSome := func(r io.Reader, n int) []byte {
		buf := make([]byte, n)
		_, err := io.ReadFull(r, buf)
		rtest.OK(t, err)
		return buf
	}

	for _, test := range []struct {
		name   string
		first  int   // bytes read before the change
		resize int64 // the file's new size
	}{
		{"shrinks in data", 32 << 10, 16 << 10},
		{"shrinks in a hole, before", 512 << 10, 100 << 10},
		{"shrinks in a hole, after", 512 << 10, 768 << 10},
		{"shrinks in a hole, to its start", 512 << 10, 64 << 10},
		{"shrinks in the trailing hole", 2*mib + 1000, 2*mib + 500},
		{"shrinks to nothing", 512 << 10, 0},
		{"grows in data", 32 << 10, 5 * mib},
		{"grows in a hole", 512 << 10, 5 * mib},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := layout.create(t, dir)
			plain, err := os.ReadFile(path)
			rtest.OK(t, err)
			f, node := openSparse(t, path)
			r := SkipHoles(f, node)
			got := readSome(r, test.first)
			rtest.OK(t, os.Truncate(path, test.resize))
			rest, err := io.ReadAll(r)
			rtest.OK(t, err)
			got = append(got, rest...)

			// what plain reading gives: the bytes read before, then the
			// file now from there on
			want := append([]byte{}, plain[:test.first]...)
			for off := int64(test.first); off < test.resize; off++ {
				if off < int64(len(plain)) {
					want = append(want, plain[off])
				} else {
					want = append(want, 0)
				}
			}
			rtest.Equals(t, len(want), len(got))
			rtest.Assert(t, bytes.Equal(want, got), "bytes differ from plain reading's")
		})
	}

	// After the end of file, data written on is read as plain reading does.
	path := layout.create(t, dir)
	f, node := openSparse(t, path)
	r := SkipHoles(f, node)
	_, err := io.ReadAll(r)
	rtest.OK(t, err)
	af, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	rtest.OK(t, err)
	_, err = af.Write([]byte("more"))
	rtest.OK(t, err)
	rtest.OK(t, af.Close())
	rest, err := io.ReadAll(r)
	rtest.OK(t, err)
	rtest.Equals(t, "more", string(rest))

	// Resized all the while, from another goroutine.
	path = layout.create(t, dir)
	f, node = openSparse(t, path)
	r = SkipHoles(f, node)
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			_ = os.Truncate(path, rnd.Int63n(4*mib))
			if wf, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
				_, _ = wf.WriteAt([]byte("data"), rnd.Int63n(4*mib))
				_ = wf.Close()
			}
		}
	}()
	var n int
	for i := 0; i < 2000; i++ {
		buf := make([]byte, 1+i%70000)
		m, err := r.Read(buf)
		n += m
		if errors.Is(err, io.EOF) {
			break
		}
		rtest.OK(t, err)
	}
	close(done)
	wg.Wait()
	t.Logf("read %d bytes of a file resized all the while", n)
}

// simFile is a file of the given contents whose data extents are given
// (sorted, apart, inside the contents; the rest is zeros), for holeReader.
// lseek fails with seekErr from call failAt on (if failAt > 0); with
// holeAtOff, SEEK_HOLE answers the offset it is given, data or not; with
// compressed, SEEK_DATA and SEEK_HOLE answer ENXIO at every offset, as
// macOS does for a file its file system compresses.
type simFile struct {
	contents []byte
	extents  [][2]int64

	failAt     int
	seekErr    error
	holeAtOff  bool
	compressed bool
	seeks      int
}

func (f *simFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(f.contents)) {
		return 0, io.EOF
	}
	n := copy(p, f.contents[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *simFile) Size() (int64, error) {
	return int64(len(f.contents)), nil
}

func (f *simFile) Seek(off int64, whence int) (int64, error) {
	f.seeks++
	switch {
	case f.failAt > 0 && f.seeks >= f.failAt:
		return 0, f.seekErr
	case f.compressed:
		return 0, unix.ENXIO
	case off >= int64(len(f.contents)):
		return 0, unix.ENXIO
	case whence == seekHole && f.holeAtOff:
		return off, nil
	}
	for _, e := range f.extents {
		if off >= e[1] {
			continue
		}
		if whence == seekData {
			return max(off, e[0]), nil
		}
		if off >= e[0] {
			return e[1], nil
		}
		return off, nil
	}
	if whence == seekData {
		return 0, unix.ENXIO
	}
	return off, nil
}

// randomSimFile returns contents of up to size bytes with random data
// extents, holes between them, and the extents.
func randomSimFile(rnd *rand.Rand, size int64) *simFile {
	f := &simFile{contents: make([]byte, 1+rnd.Int63n(size))}
	n := int64(len(f.contents))
	for off := rnd.Int63n(n); off < n; {
		end := min(n, off+1+rnd.Int63n(n/4+1))
		_, _ = rnd.Read(f.contents[off:end])
		f.extents = append(f.extents, [2]int64{off, end})
		off = end + 1 + rnd.Int63n(n/4+1)
	}
	return f
}

// An lseek that fails (EINVAL, ENOTSUP, ...) after some extents leaves the
// rest to plain reading, with the same bytes; so does SEEK_HOLE at or
// before the offset, and ENXIO where SEEK_HOLE finds no hole.
func TestSkipHolesSeekFailures(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rnd := rand.New(rand.NewSource(seed))

	readAll := func(f *simFile) (*holeReader, []byte) {
		r := &holeReader{f: f, name: "sim"}
		var out []byte
		for {
			buf := make([]byte, 1+rnd.Intn([]int{1, 4096, 100000}[rnd.Intn(3)]))
			n, err := r.Read(buf)
			out = append(out, buf[:n]...)
			if err == io.EOF {
				return r, out
			}
			rtest.OK(t, err)
		}
	}

	var skippedBeforeFailure bool
	for i := 0; i < 300; i++ {
		f := randomSimFile(rnd, 1<<20)
		_, got := readAll(f)
		rtest.Assert(t, bytes.Equal(f.contents, got), "case %d: bytes differ", i)
		seeks := f.seeks

		for _, seekErr := range []error{unix.EINVAL, unix.ENOTSUP, unix.EIO} {
			for failAt := 1; failAt <= seeks; failAt++ {
				f.seeks, f.failAt, f.seekErr = 0, failAt, seekErr
				r, got := readAll(f)
				rtest.Assert(t, bytes.Equal(f.contents, got), "case %d: bytes differ after %v at lseek %d", i, seekErr, failAt)
				rtest.Equals(t, failAt, f.seeks)
				rtest.Assert(t, r.end == plain && !r.hole, "case %d: not read plainly after %v", i, seekErr)
				skippedBeforeFailure = skippedBeforeFailure || r.holeBytes > 0
			}
		}
		f.failAt = 0

		f.seeks, f.holeAtOff = 0, true
		r, got := readAll(f)
		rtest.Assert(t, bytes.Equal(f.contents, got), "case %d: bytes differ with SEEK_HOLE at the offset", i)
		if len(f.extents) > 0 && f.extents[0][0] == 0 {
			rtest.Equals(t, int64(0), r.holeBytes)
		}
		f.holeAtOff = false

		f.seeks, f.compressed = 0, true
		r, got = readAll(f)
		rtest.Assert(t, bytes.Equal(f.contents, got), "case %d: bytes differ with ENXIO everywhere", i)
		rtest.Equals(t, int64(0), r.holeBytes)
		f.compressed = false
	}
	rtest.Assert(t, skippedBeforeFailure, "no hole was skipped before an lseek failed")

	// SEEK_HOLE before the offset: read plainly from there on.
	f := &simFile{contents: []byte("data"), extents: [][2]int64{{0, 4}}}
	r := &holeReader{f: seekHoleBefore{f}, name: "sim"}
	got, err := io.ReadAll(r)
	rtest.OK(t, err)
	rtest.Equals(t, "data", string(got))
	rtest.Assert(t, r.end == plain && !r.hole, "not read plainly")
}

// seekHoleBefore is a simFile whose SEEK_HOLE answers an offset before the
// one it is given.
type seekHoleBefore struct {
	*simFile
}

func (f seekHoleBefore) Seek(off int64, whence int) (int64, error) {
	if whence == seekHole {
		return off - 1, nil
	}
	return f.simFile.Seek(off, whence)
}

// Offsets past 4 GiB: a file of 6 GiB and 7 bytes with data across 4 GiB
// and past 5 GiB reads the same by its extents as plainly.  Its holes take
// no disk.
func TestSkipHolesPast4GiB(t *testing.T) {
	if testing.Short() {
		t.Skip("reads 12 GiB of holes")
	}
	const gib = 1 << 30
	dir := rtest.TempDir(t)
	// (ZFS gives a block to a file that is all hole)
	probe := sparseLayout{"probe", 1 << 20, nil}.create(t, dir)
	if fi, err := os.Stat(probe); err != nil || ExtendedStat(fi).Blocks*512 > 64<<10 {
		t.Skip("no holes on this file system")
	}
	rnd := rand.New(rand.NewSource(4))
	path := sparseLayout{"big", 6*gib + 7, []extent{
		{4*gib - 2, randomBytes(rnd, 70000)},
		{5*gib + 12345, randomBytes(rnd, 4096)},
	}}.create(t, dir)

	sum := func(r io.Reader) (int64, [32]byte) {
		h := sha256.New()
		n, err := io.CopyBuffer(h, r, make([]byte, 1<<20))
		rtest.OK(t, err)
		var s [32]byte
		copy(s[:], h.Sum(nil))
		return n, s
	}
	plainFile, err := os.Open(path)
	rtest.OK(t, err)
	defer func() { _ = plainFile.Close() }()
	// (without os.File's WriteTo, which copies by 32 KiB)
	wantN, want := sum(struct{ io.Reader }{plainFile})
	rtest.Equals(t, int64(6*gib+7), wantN)

	f, node := openSparse(t, path)
	r := SkipHoles(f, node)
	gotN, got := sum(r)
	rtest.Equals(t, wantN, gotN)
	rtest.Equals(t, want, got)
	hr := r.(*holeReader)
	rtest.Assert(t, hr.holeBytes >= 6*gib-1<<20, "only %d bytes of holes skipped", hr.holeBytes)
}
