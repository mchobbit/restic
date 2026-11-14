//go:build !windows

package restorer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/archiver"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// exactBackup backs up the directory src with real access times, and
// returns the snapshot of src (its tree, and its node for the target).
func exactBackup(t *testing.T, repo restic.Repository, src string) (*data.Snapshot, *data.Node) {
	t.Helper()
	local := &fs.Local{}
	arch := archiver.New(repo, local, archiver.Options{})
	arch.WithAtime = true
	sn, _, _, err := arch.Snapshot(context.Background(), []string{src}, archiver.SnapshotOptions{Time: time.Now()})
	rtest.OK(t, err)
	tree, node, err := data.FindTreeDirectoryNode(context.Background(), repo, sn.Tree, src)
	rtest.OK(t, err)
	sn.Tree = tree
	return sn, node
}

// inodes lists every entry at or below dir with its inode and change time.
// It lists directories through fs.Local, as the restorer does, which leaves
// their atimes alone (O_NOATIME): a plain listing would move them.
func inodes(t *testing.T, dir string) []string {
	t.Helper()
	var list []string
	var walk func(path string)
	walk = func(path string) {
		fi, err := os.Lstat(path)
		rtest.OK(t, err)
		ext := fs.ExtendedStat(fi)
		list = append(list, fmt.Sprintf("%v %d %v", path, ext.Inode, ext.ChangeTime.UnixNano()))
		if !fi.IsDir() {
			return
		}
		names, err := fs.Readdirnames(fs.Local{}, path, fs.O_NOFOLLOW)
		rtest.OK(t, err)
		for _, name := range names {
			walk(filepath.Join(path, name))
		}
	}
	walk(dir)
	sort.Strings(list)
	return list
}

// An --exact restore over a tree that already matches changes nothing: no
// entry's inode or change time moves, the target's either.  (Not with
// symlinks on a mount with atimes: reading one moves its atime, which the
// restore then sets again; TestRestoreExactSymlinks.)
func TestRestoreExactOverAMatchingTree(t *testing.T) {
	tmp := rtest.TempDir(t)
	src := filepath.Join(tmp, "src")
	rtest.OK(t, os.MkdirAll(filepath.Join(src, "etc", "empty"), 0o755))
	rtest.OK(t, os.WriteFile(filepath.Join(src, "etc", "cfg"), []byte("cfg"), 0o600))
	rtest.OK(t, os.WriteFile(filepath.Join(src, "big"), make([]byte, 3<<20), 0o644))
	rtest.OK(t, unix.Mkfifo(filepath.Join(src, "fifo"), 0o640))
	old := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, p := range []string{"etc/cfg", "big", "etc/empty", "etc", "."} {
		rtest.OK(t, os.Chtimes(filepath.Join(src, p), old.Add(-time.Hour), old))
	}
	repo := repository.TestRepository(t)
	sn, node := exactBackup(t, repo, src)

	dst := filepath.Join(tmp, "dst")
	for i := 0; i < 2; i++ {
		res := NewRestorer(repo, sn, Options{Exact: true})
		res.TargetNode = node
		before := inodes(t, tmp)
		ctx, cancel := context.WithCancel(context.Background())
		_, err := res.RestoreTo(ctx, dst)
		cancel()
		rtest.OK(t, err)
		if i == 1 {
			rtest.Equals(t, strings.Join(before, "\n"), strings.Join(inodes(t, tmp), "\n"), "an exact restore again")
		}
	}
	fi, err := os.Lstat(dst)
	rtest.OK(t, err)
	rtest.Assert(t, fi.ModTime().Equal(old), "the target's mtime %v", fi.ModTime())
}

// Under --exact a file with other names gets one of its own before it is
// written or given metadata: the other names keep their contents and
// metadata.  The snapshot's hard links are made, and kept when they are
// there.
func TestRestoreExactHardLinks(t *testing.T) {
	repo := repository.TestRepository(t)
	for _, test := range []struct {
		name string
		file File
	}{
		{"other contents", File{Data: "new", Mode: 0o644}},
		{"other mode", File{Data: "old", Mode: 0o600}},
	} {
		sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: map[string]Node{"a": test.file}}, noopGetGenericAttributes)
		tmp := rtest.TempDir(t)
		dst, outside := filepath.Join(tmp, "dst"), filepath.Join(tmp, "outside")
		rtest.OK(t, os.Mkdir(dst, 0o755))
		rtest.OK(t, os.WriteFile(outside, []byte("old"), 0o644))
		rtest.OK(t, os.Link(outside, filepath.Join(dst, "a")))

		exactRestore(t, repo, sn, dst)
		got, err := os.ReadFile(filepath.Join(dst, "a"))
		rtest.OK(t, err)
		rtest.Equals(t, test.file.Data, string(got), test.name)
		got, err = os.ReadFile(outside)
		rtest.OK(t, err)
		rtest.Equals(t, "old", string(got), test.name+": the other name's contents")
		fi, err := os.Lstat(outside)
		rtest.OK(t, err)
		rtest.Equals(t, os.FileMode(0o644), fi.Mode().Perm(), test.name+": the other name's mode")
		ai, err := os.Lstat(filepath.Join(dst, "a"))
		rtest.OK(t, err)
		rtest.Equals(t, test.file.Mode, ai.Mode().Perm(), test.name+": the mode")
	}

	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: map[string]Node{
		"a": File{Data: "x", Links: 2, Inode: 7},
		"b": File{Data: "x", Links: 2, Inode: 7},
	}}, noopGetGenericAttributes)
	dst := rtest.TempDir(t)
	rtest.OK(t, os.WriteFile(filepath.Join(dst, "a"), []byte("x"), 0o644))
	rtest.OK(t, os.WriteFile(filepath.Join(dst, "b"), []byte("x"), 0o644))
	for i := 0; i < 2; i++ {
		exactRestore(t, repo, sn, dst)
		rtest.Assert(t, sameFile(filepath.Join(dst, "a"), filepath.Join(dst, "b")), "round %d: a and b not linked", i)
	}
}

// Under --exact a symlink to the same target stays; one to another is
// replaced.
func TestRestoreExactSymlinks(t *testing.T) {
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: map[string]Node{
		"same":  Symlink{Target: "t"},
		"other": Symlink{Target: "u"},
	}}, noopGetGenericAttributes)
	dst := rtest.TempDir(t)
	rtest.OK(t, os.Symlink("t", filepath.Join(dst, "same")))
	rtest.OK(t, os.Symlink("v", filepath.Join(dst, "other")))
	before, err := os.Lstat(filepath.Join(dst, "same"))
	rtest.OK(t, err)

	exactRestore(t, repo, sn, dst)
	after, err := os.Lstat(filepath.Join(dst, "same"))
	rtest.OK(t, err)
	rtest.Assert(t, os.SameFile(before, after), "the same symlink replaced")
	link, err := os.Readlink(filepath.Join(dst, "other"))
	rtest.OK(t, err)
	rtest.Equals(t, "u", link)
}
