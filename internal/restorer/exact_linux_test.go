package restorer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// With root, on a loop btrfs, where files clone: a broken hard link is a
// clone, and an exact restore over a matching tree moves no change time.
func TestRestoreExactBtrfs(t *testing.T) {
	mnt := mountLoopFS(t, 512<<20, "mkfs.btrfs", "-q")
	old := rtest.TestTempDir
	rtest.TestTempDir = mnt
	t.Cleanup(func() { rtest.TestTempDir = old })

	src, dst := filepath.Join(mnt, "src"), filepath.Join(mnt, "dst")
	rtest.OK(t, os.WriteFile(src, []byte("probe"), 0o644))
	rtest.OK(t, fs.CloneFile(src, dst, 5))
	rtest.OK(t, os.Link(dst, src+"2"))
	cloned, err := fs.BreakHardLink(dst)
	rtest.OK(t, err)
	rtest.Assert(t, cloned, "a hard link on btrfs not broken by a clone")

	t.Run("OverAMatchingTree", TestRestoreExactOverAMatchingTree)
	t.Run("HardLinks", TestRestoreExactHardLinks)
	t.Run("Label", TestRestoreExactLabel)
}

// Under --exact an SELinux label the node hasn't got is removed (a btrfs
// snapshot made exact inherits its base's); elsewhere it stays.
func TestRestoreExactLabel(t *testing.T) {
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: map[string]Node{"f": File{Data: "f"}}}, noopGetGenericAttributes)
	const label = "system_u:object_r:etc_t:s0"
	for _, exact := range []bool{false, true} {
		dst := rtest.TempDir(t)
		f := filepath.Join(dst, "f")
		rtest.OK(t, os.WriteFile(f, []byte("f"), 0o644))
		if err := unix.Lsetxattr(f, selinuxLabel, []byte(label), 0); err != nil {
			t.Skipf("no SELinux label can be set here: %v", err)
		}
		res := NewRestorer(repo, sn, Options{Exact: exact})
		ctx, cancel := context.WithCancel(context.Background())
		_, err := res.RestoreTo(ctx, dst)
		cancel()
		rtest.OK(t, err)
		_, err = unix.Lgetxattr(f, selinuxLabel, nil)
		rtest.Assert(t, exact == (err != nil), "exact %v: label removed: %v (%v)", exact, err != nil, err)
	}
}
