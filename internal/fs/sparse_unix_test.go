//go:build !windows

package fs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestNodeFillSparse(t *testing.T) {
	dir := rtest.TempDir(t)
	create := func(name string, fill func(f *os.File) error) (string, *ExtendedFileInfo) {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		rtest.OK(t, err)
		rtest.OK(t, fill(f))
		rtest.OK(t, f.Close())
		fi, err := os.Lstat(path)
		rtest.OK(t, err)
		return path, ExtendedStat(fi)
	}
	sparse := func(path string, fi *ExtendedFileInfo) bool {
		node, err := nodeFromFileInfo(path, fi, false, t.Logf)
		rtest.OK(t, err)
		return node.Sparse()
	}

	path, fi := create("dense", func(f *os.File) error {
		_, err := f.Write(make([]byte, 1<<20))
		return err
	})
	rtest.Assert(t, !sparse(path, fi), "dense file marked sparse")

	path, fi = create("holes", func(f *os.File) error {
		if _, err := f.WriteAt([]byte("data"), 1<<19); err != nil {
			return err
		}
		return f.Truncate(1 << 20)
	})
	if fi.Blocks*512 >= fi.Size {
		t.Logf("no holes on this file system (%d blocks)", fi.Blocks)
	} else {
		rtest.Assert(t, sparse(path, fi), "file with holes not marked sparse")
	}

	// fallocate(2) leaves unwritten extents, which SEEK_HOLE may report as
	// holes, but which count as blocks.
	if runtime.GOOS == "linux" {
		path, fi = create("preallocated", func(f *os.File) error {
			return PreallocateFile(f, 1<<20)
		})
		if fi.Blocks*512 < fi.Size {
			t.Logf("no preallocation on this file system (%d blocks)", fi.Blocks)
		} else {
			rtest.Assert(t, !sparse(path, fi), "preallocated file marked sparse")
		}
	}
}
