//go:build !windows

package fs

import (
	"bytes"
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
		sparse, err := node.Sparse()
		rtest.OK(t, err)
		return sparse
	}

	// Not zeros: a file system that compresses, or drops zero blocks,
	// still allocates it in full.
	path, fi := create("dense", func(f *os.File) error {
		_, err := f.Write(bytes.Repeat([]byte{1}, 1<<20))
		return err
	})
	rtest.Assert(t, !sparse(path, fi), "dense file marked sparse")

	holes := func(f *os.File) error {
		if _, err := f.WriteAt([]byte("data"), 1<<19); err != nil {
			return err
		}
		return f.Truncate(1 << 20)
	}
	path, fi = create("holes", holes)
	if fi.Blocks*512 >= fi.Size {
		t.Logf("no holes on this file system (%d blocks)", fi.Blocks)
	} else {
		rtest.Assert(t, sparse(path, fi), "file with holes not marked sparse")
	}

	// A file replaced since its lstat(2) is left unmarked, where the OS
	// probes it.
	_, fiOld := create("replaced", holes)
	path, fi = create("replacement", holes)
	if fi.Blocks*512 < fi.Size && seekHole >= 0 {
		rtest.Assert(t, !sparse(path, fiOld), "a file other than the one lstat saw marked sparse")
	}

	// fallocate(2) leaves unwritten extents, which SEEK_HOLE may report as
	// holes, but which count as blocks.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		path, fi = create("preallocated", func(f *os.File) error {
			if err := PreallocateFile(f, 1<<20); err != nil {
				t.Skipf("no preallocation here: %v", err)
			}
			return nil
		})
		if fi.Blocks*512 < fi.Size {
			t.Logf("no preallocation on this file system (%d blocks)", fi.Blocks)
		} else {
			rtest.Assert(t, !sparse(path, fi), "preallocated file marked sparse")
		}
	}
}

// A file with holes that can't be opened isn't marked: the probe (and on
// Linux the inode flags and project ID with it) needs the file open.
func TestNodeFillSparseUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every file")
	}
	path := filepath.Join(rtest.TempDir(t), "holes")
	f, err := os.Create(path)
	rtest.OK(t, err)
	_, err = f.WriteAt([]byte("data"), 1<<19)
	rtest.OK(t, err)
	rtest.OK(t, f.Truncate(1<<20))
	rtest.OK(t, f.Close())
	rtest.OK(t, os.Chmod(path, 0))
	fi, err := os.Lstat(path)
	rtest.OK(t, err)
	efi := ExtendedStat(fi)
	if efi.Blocks*512 >= efi.Size {
		t.Skipf("no holes on this file system (%d blocks)", efi.Blocks)
	}
	if seekHole < 0 {
		t.Skip("no SEEK_HOLE probe here: the blocks decide")
	}

	node, err := nodeFromFileInfo(path, efi, false, t.Logf)
	rtest.OK(t, err)
	sparse, err := node.Sparse()
	rtest.OK(t, err)
	rtest.Assert(t, !sparse, "unreadable file marked sparse")
}
