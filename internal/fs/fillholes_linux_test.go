//go:build linux

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// A fill that runs out of space (ENOSPC) is returned as an error, but the
// file is looked at again: ext4 keeps what fallocate allocated before it
// failed, so the file got blocks and has changed (the restorer reports it
// as updated, with a warning).
func TestFillHolesENOSPC(t *testing.T) {
	mnt := mountLoop(t, 16<<20, "mkfs.ext4", "-q")
	path := filepath.Join(mnt, "holes")
	const size = 64 << 20
	f, err := os.Create(path)
	rtest.OK(t, err)
	_, err = f.WriteAt([]byte("x"), 0)
	rtest.OK(t, err)
	rtest.OK(t, f.Truncate(size))
	rtest.OK(t, f.Close())

	var before, after unix.Stat_t
	rtest.OK(t, unix.Lstat(path, &before))
	rtest.Assert(t, HasHolesToFill(path, size), "no holes to fill: %d blocks", before.Blocks)

	filled, err := FillHoles(path, size)
	rtest.Assert(t, errors.Is(err, unix.ENOSPC), "want ENOSPC, got %v", err)
	rtest.OK(t, unix.Lstat(path, &after))
	t.Logf("blocks %d -> %d, filled %v", before.Blocks, after.Blocks, filled)
	rtest.Equals(t, after.Blocks > before.Blocks, filled)
	rtest.Assert(t, filled, "ext4 kept no blocks of the failed fallocate (%d -> %d)", before.Blocks, after.Blocks)
}

// A fill that works reports the file as changed; a file without holes is
// not opened for writing and reported unchanged.
func TestFillHoles(t *testing.T) {
	dir := rtest.TempDir(t)
	path := filepath.Join(dir, "holes")
	const size = 4 << 20
	f, err := os.Create(path)
	rtest.OK(t, err)
	rtest.OK(t, f.Truncate(size))
	rtest.OK(t, f.Close())
	var st unix.Stat_t
	rtest.OK(t, unix.Lstat(path, &st))
	if st.Blocks*512 >= size || !HasHolesToFill(path, size) {
		t.Skip("no holes to fill on this file system (or ZFS)")
	}

	filled, err := FillHoles(path, size)
	if errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip(err)
	}
	rtest.OK(t, err)
	rtest.Assert(t, filled, "holes not filled")
	rtest.OK(t, unix.Lstat(path, &st))
	rtest.Assert(t, st.Blocks*512 >= size, "holes left: %d blocks", st.Blocks)

	filled, err = FillHoles(path, size)
	rtest.OK(t, err)
	rtest.Assert(t, !filled, "a file without holes was filled")
}

// TestAllocatesType: btrfs allocates only a NOCOW file, bcachefs none, the
// others every file (ZFS too: fileHoles leaves its holes alone).
func TestAllocatesType(t *testing.T) {
	for _, c := range []struct {
		name  string
		typ   uint32
		nocow bool
		want  bool
	}{
		{"btrfs", btrfsSuperMagic, false, false},
		{"btrfs NOCOW", btrfsSuperMagic, true, true},
		{"bcachefs", bcachefsSuperMagic, false, false},
		{"bcachefs nocow", bcachefsSuperMagic, true, false},
		{"ext4", 0xef53, false, true},
		{"XFS", 0x58465342, false, true},
		{"ZFS", zfsSuperMagic, false, true},
	} {
		if got := allocatesType(c.typ, func() bool { return c.nocow }); got != c.want {
			t.Errorf("%v: allocates %v, want %v", c.name, got, c.want)
		}
	}
}
