//go:build linux || freebsd || darwin || illumos

package archiver

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

// A file with holes, which is read by its data extents, gets the same blobs
// as reading it plainly gives: the same chunk boundaries and blob IDs.
func TestFileSaverHolesSameBlobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rnd := rand.New(rand.NewSource(seed))
	dir := test.TempDir(t)
	const mib = 1 << 20

	var files []string
	for i := 0; i < 16; i++ {
		path := filepath.Join(dir, "file"+string(rune('a'+i)))
		f, err := os.Create(path)
		test.OK(t, err)
		size := rnd.Int63n(24 * mib)
		for j := rnd.Intn(10); j > 0; j-- {
			buf := make([]byte, 1+rnd.Intn([]int{100, 8000, 900000, 4 * mib}[rnd.Intn(4)]))
			_, _ = rnd.Read(buf)
			_, err := f.WriteAt(buf, rnd.Int63n(size+1))
			test.OK(t, err)
		}
		if rnd.Intn(3) == 0 {
			_, err := f.WriteAt([]byte("end"), max(0, size-3))
			test.OK(t, err)
		}
		test.OK(t, f.Truncate(max(size, rnd.Int63n(size+1))))
		test.OK(t, f.Close())
		files = append(files, path)
	}

	s, _, ctx, wg := startFileSaver(ctx, t, fs.Local{})
	var results []futureNode
	for _, path := range files {
		f, err := fs.Local{}.OpenFile(path, fs.O_RDONLY|fs.O_NOFOLLOW, false)
		test.OK(t, err)
		results = append(results, s.Save(ctx, path, path, f, func() {}, func() {}, func(*data.Node, ItemStats) {}))
	}

	sparse := 0
	for i, result := range results {
		fnr := result.take(ctx)
		test.OK(t, fnr.err)
		if marked, _ := fnr.node.Sparse(); marked {
			sparse++
		}

		plain, err := os.ReadFile(files[i])
		test.OK(t, err)
		var want restic.IDs
		chnker := chunker.New(bytes.NewReader(plain), s.pol)
		buf := make([]byte, chunker.MaxSize)
		for {
			c, err := chnker.Next(buf)
			if err == io.EOF {
				break
			}
			test.OK(t, err)
			want = append(want, restic.Hash(c.Data))
		}
		if want == nil {
			want = restic.IDs{}
		}
		test.Equals(t, want, restic.IDs(fnr.node.Content))
		test.Equals(t, uint64(len(plain)), fnr.node.Size)
	}
	t.Logf("%d of %d files marked sparse", sparse, len(files))

	s.TriggerShutdown()
	test.OK(t, wg.Wait())
}
