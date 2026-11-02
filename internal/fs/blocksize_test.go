package fs

import (
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

// Holes are made in blocks of at least 512 bytes, the file system's own.
func TestHoleBlockSize(t *testing.T) {
	f, err := os.Create(filepath.Join(rtest.TempDir(t), "file"))
	rtest.OK(t, err)
	defer func() { _ = f.Close() }()
	bs := HoleBlockSize(f)
	t.Logf("block size %d", bs)
	rtest.Assert(t, bs >= 512, "block size %d is less than 512", bs)
}

// The file system's block size, at least 512 (bcachefs with block_size
// 512 has holes that small); 4096 where it isn't known.
func TestHoleBlockSizeRule(t *testing.T) {
	for _, c := range []struct{ bs, want int64 }{
		{-1, 4096}, {0, 4096}, {1, 512}, {511, 512}, {512, 512}, {1024, 1024},
		{4096, 4096}, {65536, 65536}, {131072, 131072},
	} {
		rtest.Equals(t, c.want, holeBlockSize(c.bs))
	}
}
