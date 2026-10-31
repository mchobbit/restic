package restorer

import (
	"bytes"
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

// writeSparse writes all of a blob but the blocks of zeros wholly inside it,
// aligned to the file offset, which it leaves out, and writes nothing twice.
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

		end := offset + int64(n)
		file := make([]byte, end)
		written := make([]bool, end)
		var writes int
		got, err := writeSparse(func(q []byte, off int64) (int, error) {
			rtest.Assert(t, len(q) > 0, "empty write at %d", off)
			rtest.Assert(t, off >= offset && off+int64(len(q)) <= end, "write of [%d, %d) outside [%d, %d)", off, off+int64(len(q)), offset, end)
			for j := range q {
				if written[off+int64(j)] {
					t.Fatalf("byte %d written twice", off+int64(j))
				}
				written[off+int64(j)] = true
			}
			copy(file[off:], q)
			writes++
			return len(q), nil
		}, p, offset, bs)
		rtest.OK(t, err)
		rtest.Equals(t, n, got)
		rtest.Assert(t, bytes.Equal(p, file[offset:]), "case %d: contents differ", i)

		zeroBlocks := 0
		for block := offset / bs * bs; block < end; block += bs {
			whole := block >= offset && block+bs <= end
			zero := whole && bytes.Count(p[block-offset:block+bs-offset], []byte{0}) == int(bs)
			if zero {
				zeroBlocks++
			}
			for pos := max(block, offset); pos < min(block+bs, end); pos++ {
				if zero == written[pos] {
					t.Fatalf("case %d (offset %d, %d bytes, blocks of %d): byte %d written %v, but in a whole zero block %v",
						i, offset, n, bs, pos, written[pos], zero)
				}
			}
		}
		rtest.Assert(t, writes <= zeroBlocks+1, "case %d: %d writes around %d zero blocks", i, writes, zeroBlocks)
	}
}
