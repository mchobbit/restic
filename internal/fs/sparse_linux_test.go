package fs

import (
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// On JFS and nilfs2, SEEK_HOLE finds no hole before the size, so the blocks
// decide (S3).
func TestGenericSeekHole(t *testing.T) {
	path := filepath.Join(rtest.TempDir(t), "file")
	rtest.OK(t, os.WriteFile(path, []byte("x"), 0o600))
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()

	// Devices no file is on, with the f_type cached as if fstatfs gave it.
	for i, test := range []struct {
		magic uint32
		want  bool
	}{
		{jfsSuperMagic, true},
		{nilfsSuperMagic, true},
		{unix.EXT4_SUPER_MAGIC, false},
		{unix.TMPFS_MAGIC, false},
	} {
		dev := uint64(1)<<63 | uint64(i)
		fsTypes.Store(dev, test.magic)
		defer fsTypes.Delete(dev)
		rtest.Equals(t, test.want, genericSeekHole(fd, dev))
	}
}

// BKO_TEST_GENERIC_SEEK_HOLE_DIR names a directory on JFS or nilfs2: a file
// with holes there is marked sparse though SEEK_HOLE finds none.
func TestNodeFillSparseGenericSeekHole(t *testing.T) {
	dir := os.Getenv("BKO_TEST_GENERIC_SEEK_HOLE_DIR")
	if dir == "" {
		t.Skip("BKO_TEST_GENERIC_SEEK_HOLE_DIR not set")
	}
	dir, err := os.MkdirTemp(dir, "bko-")
	rtest.OK(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	fill := func(name string, off int64, data []byte) (string, *ExtendedFileInfo) {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		rtest.OK(t, err)
		_, err = f.WriteAt(data, off)
		rtest.OK(t, err)
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

	path, fi := fill("holes", 32<<20, []byte("y"))
	f, err := os.Open(path)
	rtest.OK(t, err)
	hole, err := f.Seek(0, unix.SEEK_HOLE)
	rtest.OK(t, f.Close())
	rtest.OK(t, err)
	rtest.Assert(t, hole == fi.Size, "SEEK_HOLE found a hole at %d: not a generic SEEK_HOLE", hole)
	rtest.Assert(t, fi.Blocks*512 < fi.Size, "no holes: %d blocks", fi.Blocks)
	rtest.Assert(t, sparse(path, fi), "file with holes not marked sparse")

	path, fi = fill("dense", 0, make([]byte, 1<<20))
	rtest.Assert(t, !sparse(path, fi), "dense file marked sparse")
}
