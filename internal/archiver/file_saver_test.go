package archiver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
	"golang.org/x/sync/errgroup"
)

func createTestFiles(t testing.TB, num int) (files []string) {
	tempdir := test.TempDir(t)

	for i := 0; i < num; i++ {
		filename := fmt.Sprintf("testfile-%d", i)
		err := os.WriteFile(filepath.Join(tempdir, filename), []byte(filename), 0600)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, filepath.Join(tempdir, filename))
	}

	return files
}

func startFileSaver(ctx context.Context, t testing.TB, _ fs.FS) (*fileSaver, *mockSaver, context.Context, *errgroup.Group) {
	wg, ctx := errgroup.WithContext(ctx)

	workers := uint(runtime.NumCPU())
	pol, err := chunker.RandomPolynomial()
	if err != nil {
		t.Fatal(err)
	}

	saver := &mockSaver{saved: make(map[string]int)}
	s := newFileSaver(ctx, wg, saver, pol, workers)
	s.NodeFromFileInfo = func(snPath, filename string, meta ToNoder, ignoreXattrListError bool) (*data.Node, error) {
		return meta.ToNode(ignoreXattrListError, t.Logf)
	}

	return s, saver, ctx, wg
}

func TestFileSaver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startFn := func() {}
	completeReadingFn := func() {}
	completeFn := func(*data.Node, ItemStats) {}

	files := createTestFiles(t, 15)
	testFs := fs.Local{}
	s, saver, ctx, wg := startFileSaver(ctx, t, testFs)

	var results []futureNode

	for _, filename := range files {
		f, err := testFs.OpenFile(filename, os.O_RDONLY, false)
		if err != nil {
			t.Fatal(err)
		}

		ff := s.Save(ctx, filename, filename, f, startFn, completeReadingFn, completeFn)
		results = append(results, ff)
	}

	for _, file := range results {
		fnr := file.take(ctx)
		if fnr.err != nil {
			t.Errorf("unable to save file: %v", fnr.err)
		}
	}

	test.Assert(t, len(saver.saved) == len(files), "expected %d saved files, got %d", len(files), len(saver.saved))

	s.TriggerShutdown()

	err := wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

// The IDs of all-zero chunks, kept, are their hashes all the same, and
// other chunks are left to the uploader to hash (BackOtter).
func TestZeroChunkID(t *testing.T) {
	almostEnd := make([]byte, 4096)
	almostEnd[4095] = 1
	almostStart := make([]byte, 4096)
	almostStart[0] = 1
	ids := map[int]restic.ID{}
	for i := 0; i < 2; i++ {
		for _, chunk := range [][]byte{nil, {0}, {1}, make([]byte, 4096), make([]byte, chunker.MinSize), almostEnd, almostStart} {
			want := restic.Hash(chunk)
			if restic.ZeroPrefixLen(chunk) != len(chunk) {
				want = restic.ID{}
			}
			test.Equals(t, want, zeroChunkID(chunk, ids))
		}
	}
	test.Equals(t, 4, len(ids))
}
