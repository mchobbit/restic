package fs

import (
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

// Holes are made in blocks of at least 4096 bytes.
func TestHoleBlockSize(t *testing.T) {
	f, err := os.Create(filepath.Join(rtest.TempDir(t), "file"))
	rtest.OK(t, err)
	defer func() { _ = f.Close() }()
	bs := HoleBlockSize(f)
	t.Logf("block size %d", bs)
	rtest.Assert(t, bs >= 4096, "block size %d is less than 4096", bs)
}
