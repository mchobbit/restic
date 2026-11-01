package restorer

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

// randomBlob returns n bytes of runs of zeros and of data, some of them
// lining up with blocks of bs bytes from offset.
func randomBlob(rnd *rand.Rand, n int, offset, bs int64) []byte {
	p := make([]byte, n)
	for i := 0; i < n; {
		run := 1 + rnd.Intn(int(3*bs))
		if rnd.Intn(3) == 0 {
			// up to a block boundary
			run = int(bs - (offset+int64(i))%bs + int64(rnd.Intn(3))*bs)
		}
		run = min(run, n-i)
		if rnd.Intn(2) == 0 {
			_, _ = rnd.Read(p[i : i+run])
			if rnd.Intn(4) == 0 {
				// data with a single byte that isn't zero
				clear(p[i : i+run])
				p[i+rnd.Intn(run)] = 1
			}
		}
		i += run
	}
	return p
}

// sparseWrites records what writeSparse writes of p at offset into a file of
// zeros, in blocks of bs bytes: the file then, the writes, and which bytes
// were written.  A write outside p, an empty one or a byte written twice
// fails the test.
func sparseWrites(t *testing.T, p []byte, offset, bs int64) (file []byte, writes [][2]int64, written []bool) {
	t.Helper()
	end := offset + int64(len(p))
	// room around p for writes that shouldn't be there
	file = make([]byte, end+2*bs)
	written = make([]bool, len(file))
	got, err := writeSparse(func(q []byte, off int64) (int, error) {
		rtest.Assert(t, len(q) > 0, "empty write at %d", off)
		rtest.Assert(t, off >= offset && off+int64(len(q)) <= end, "write of [%d, %d) outside the blob's [%d, %d)", off, off+int64(len(q)), offset, end)
		for j := range q {
			if written[off+int64(j)] {
				t.Fatalf("byte %d written twice", off+int64(j))
			}
			written[off+int64(j)] = true
		}
		copy(file[off:], q)
		writes = append(writes, [2]int64{off, off + int64(len(q))})
		return len(q), nil
	}, p, offset, bs)
	rtest.OK(t, err)
	rtest.Equals(t, len(p), got)
	return file, writes, written
}

// Pieces of a blob at its ends that are all zeros within their block are
// left out; those with a byte other than zero are written exactly, never
// past the blob.
func TestWriteSparseEdges(t *testing.T) {
	const bs = 8
	data := []byte("datadata")
	for _, test := range []struct {
		name   string
		offset int64
		p      []byte
		writes [][2]int64
	}{
		{"zero edges", 5, append(append(make([]byte, 3), data...), make([]byte, 4)...), [][2]int64{{8, 16}}},
		{"data edges", 5, append(append([]byte{0, 0, 1}, data...), 0, 2, 0, 0), [][2]int64{{5, 20}}},
		{"zero head, data tail", 5, append(append(make([]byte, 3), data...), 0, 0, 0, 3), [][2]int64{{8, 20}}},
		{"inside one block, zeros", 9, make([]byte, 5), nil},
		{"inside one block, data", 9, []byte{0, 0, 4, 0, 0}, [][2]int64{{9, 14}}},
		{"zero block between", 5, append(append([]byte{0, 0, 1}, make([]byte, 8)...), data...), [][2]int64{{5, 8}, {16, 24}}},
		{"aligned", 8, append(append(append([]byte{}, data...), make([]byte, 8)...), data...), [][2]int64{{8, 16}, {24, 32}}},
		{"empty", 3, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			file, writes, _ := sparseWrites(t, test.p, test.offset, bs)
			rtest.Equals(t, test.writes, writes)
			want := make([]byte, len(file))
			copy(want[test.offset:], test.p)
			rtest.Equals(t, want, file)
		})
	}
}

// writeSparse writes, of a blob, the pieces within one block of bs bytes,
// aligned to the file offset, that have a byte other than zero, exactly:
// the file it writes into, zeros at first, then holds the blob at its
// offset and zeros everywhere else, and the blocks written into are those
// where the blob has a byte other than zero.
func TestWriteSparse(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rnd := rand.New(rand.NewSource(seed))

	for i := 0; i < 2000; i++ {
		bs := []int64{1, 2, 7, 16, 512, 4096, 131072}[rnd.Intn(7)]
		offset := rnd.Int63n(4 * bs)
		if rnd.Intn(3) == 0 {
			offset = offset / bs * bs
		}
		n := rnd.Intn([]int{3, 100, int(8 * bs), 100000}[rnd.Intn(4)])
		p := randomBlob(rnd, n, offset, bs)
		if rnd.Intn(10) == 0 {
			clear(p)
		}

		file, writes, written := sparseWrites(t, p, offset, bs)
		end := offset + int64(n)
		want := make([]byte, len(file))
		copy(want[offset:], p)
		rtest.Assert(t, bytes.Equal(want, file), "case %d (offset %d, %d bytes, blocks of %d): file isn't the blob on zeros", i, offset, n, bs)

		// Each block's part of p: written in full if it has a byte other
		// than zero, else not at all.  Runs of written parts: one write
		// each.
		runs := 0
		prevWritten := false
		for block := offset / bs * bs; block < end; block += bs {
			lo, hi := max(block, offset), min(block+bs, end)
			data := bytes.Count(p[lo-offset:hi-offset], []byte{0}) != int(hi-lo)
			for pos := lo; pos < hi; pos++ {
				if data != written[pos] {
					t.Fatalf("case %d (offset %d, %d bytes, blocks of %d): byte %d written %v, but its block has data %v",
						i, offset, n, bs, pos, written[pos], data)
				}
			}
			if data && !prevWritten {
				runs++
			}
			prevWritten = data
		}
		rtest.Assert(t, len(writes) == runs, "case %d: %d writes for %d runs of blocks with data", i, len(writes), runs)
	}
}

// A failed write gives the bytes of p before it and those it wrote.
func TestWriteSparseError(t *testing.T) {
	p := append(append([]byte("data"), make([]byte, 8)...), "more data"...)
	fail := errors.New("no space")
	n, err := writeSparse(func(q []byte, off int64) (int, error) {
		if off == 0 {
			return len(q), nil
		}
		return 3, fail
	}, p, 0, 4)
	rtest.Assert(t, errors.Is(err, fail), "error %v", err)
	rtest.Equals(t, 12+3, n)
}
