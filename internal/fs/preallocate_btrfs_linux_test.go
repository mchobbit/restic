//go:build linux

package fs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// With root, on a loop btrfs (rule S5): PreallocateFile only sizes a file,
// but allocates a NOCOW one; a file's holes are filled only if it is NOCOW,
// and a file whose holes wouldn't be is neither opened nor counted.
func TestPreallocateBtrfs(t *testing.T) {
	mnt := mountLoop(t, 256<<20, "mkfs.btrfs", "-q")
	if _, err := exec.LookPath("chattr"); err != nil {
		t.Skip("no chattr")
	}
	const size = 4 << 20
	create := func(name string, nocow bool) (string, *os.File) {
		path := filepath.Join(mnt, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		rtest.OK(t, err)
		t.Cleanup(func() { _ = f.Close() })
		if nocow {
			if out, err := exec.Command("chattr", "+C", path).CombinedOutput(); err != nil {
				t.Fatalf("chattr +C: %v: %s", err, out)
			}
		}
		return path, f
	}
	stat := func(path string) unix.Stat_t {
		var st unix.Stat_t
		rtest.OK(t, unix.Stat(path, &st))
		return st
	}

	plain, f := create("plain", false)
	rtest.OK(t, PreallocateFile(f, size))
	st := stat(plain)
	rtest.Equals(t, int64(size), st.Size)
	rtest.Assert(t, st.Blocks*512 < size, "plain file allocated: %v blocks", st.Blocks)
	rtest.Assert(t, !HasHolesToFill(plain, size), "plain file's holes to fill")
	filled, err := FillHoles(plain, size)
	rtest.OK(t, err)
	rtest.Assert(t, !filled, "plain file's holes filled")
	rtest.Assert(t, stat(plain).Blocks*512 < size, "plain file allocated by FillHoles")

	nocow, f := create("nocow", true)
	rtest.OK(t, PreallocateFile(f, size))
	st = stat(nocow)
	rtest.Equals(t, int64(size), st.Size)
	rtest.Assert(t, st.Blocks*512 >= size, "NOCOW file not allocated: %v blocks", st.Blocks)

	holes, f := create("holes", true)
	rtest.OK(t, f.Truncate(size))
	rtest.Assert(t, HasHolesToFill(holes, size), "NOCOW file's holes not to fill")
	filled, err = FillHoles(holes, size)
	rtest.OK(t, err)
	rtest.Assert(t, filled, "NOCOW file's holes not filled")
	rtest.Assert(t, stat(holes).Blocks*512 >= size, "NOCOW file not allocated by FillHoles")
}
