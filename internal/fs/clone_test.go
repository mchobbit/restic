//go:build !windows

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

// cloneTouched tells whether err is a CloneFile error that touched dst.
func cloneTouched(t *testing.T, err error) bool {
	t.Helper()
	var cerr *CloneError
	rtest.Assert(t, errors.As(err, &cerr), "not a CloneError: %v", err)
	return cerr.Touched
}

// A file is never cloned onto itself, or onto another of its names:
// opening the destination empties it, and with it the source, and a file
// system that clones then clones nothing and succeeds (btrfs, XFS),
// leaving an empty file.
func TestCloneFileNeverOntoItsSource(t *testing.T) {
	dir := rtest.TempDir(t)
	f, g := filepath.Join(dir, "f"), filepath.Join(dir, "g")
	rtest.OK(t, os.WriteFile(f, []byte("snapshot 12345"), 0o644))
	rtest.OK(t, os.Link(f, g))
	for _, dst := range []string{f, g} {
		err := CloneFile(f, dst, 14)
		rtest.Assert(t, err != nil, "%v: cloned onto its source", dst)
		rtest.Assert(t, !cloneTouched(t, err), "%v: touched", dst)
		data, err := os.ReadFile(f)
		rtest.OK(t, err)
		rtest.Equals(t, "snapshot 12345", string(data), dst)
	}
}

// A source of another size, or none, leaves the destination as it was; so
// does a destination with other names, which would all get the contents.
func TestCloneFileUntouched(t *testing.T) {
	dir := rtest.TempDir(t)
	src, dst, other := filepath.Join(dir, "src"), filepath.Join(dir, "dst"), filepath.Join(dir, "other")
	rtest.OK(t, os.WriteFile(src, []byte("source"), 0o644))
	rtest.OK(t, os.WriteFile(dst, []byte("destination"), 0o644))

	for _, test := range []struct {
		name     string
		src, dst string
		size     int64
	}{
		{"wrong size", src, dst, 5},
		{"no source", filepath.Join(dir, "missing"), dst, 6},
		{"a directory", dir, dst, 6},
	} {
		err := CloneFile(test.src, test.dst, test.size)
		rtest.Assert(t, err != nil && !cloneTouched(t, err), "%v: %v", test.name, err)
		data, err := os.ReadFile(dst)
		rtest.OK(t, err)
		rtest.Equals(t, "destination", string(data), test.name)
	}

	rtest.OK(t, os.Link(dst, other))
	err := CloneFile(src, dst, 6)
	rtest.Assert(t, err != nil && !cloneTouched(t, err), "other names: %v", err)
	data, err := os.ReadFile(other)
	rtest.OK(t, err)
	rtest.Equals(t, "destination", string(data))
}

// reflinksWork tells whether dir's file system clones files (FICLONE).
func reflinksWork(t *testing.T, dir string) bool {
	t.Helper()
	src, dst := filepath.Join(dir, ".probe-src"), filepath.Join(dir, ".probe-dst")
	rtest.OK(t, os.WriteFile(src, []byte("probe"), 0o600))
	defer func() { _ = os.Remove(src); _ = os.Remove(dst) }()
	return CloneFile(src, dst, 5) == nil
}

// A file with other names gets a name of its own: a clone of it where the
// file system clones, else nothing (removed).  The other names keep the old
// file, contents and all.
func TestBreakHardLink(t *testing.T) {
	dir := rtest.TempDir(t)
	f, g := filepath.Join(dir, "f"), filepath.Join(dir, "g")
	rtest.OK(t, os.WriteFile(f, []byte("shared"), 0o640))
	rtest.OK(t, os.Link(f, g))

	cloned, err := BreakHardLink(f)
	rtest.OK(t, err)
	rtest.Equals(t, reflinksWork(t, dir), cloned)
	gi, err := os.Lstat(g)
	rtest.OK(t, err)
	rtest.Equals(t, uint64(1), ExtendedStat(gi).Links, "g's links")
	data, err := os.ReadFile(g)
	rtest.OK(t, err)
	rtest.Equals(t, "shared", string(data))

	fi, err := os.Lstat(f)
	if !cloned {
		rtest.Assert(t, errors.Is(err, os.ErrNotExist), "f not removed: %v", err)
		return
	}
	rtest.OK(t, err)
	rtest.Assert(t, !os.SameFile(fi, gi), "f is still g")
	data, err = os.ReadFile(f)
	rtest.OK(t, err)
	rtest.Equals(t, "shared", string(data))
	_, err = os.Lstat(filepath.Join(dir, ".bko-exact-f"))
	rtest.Assert(t, errors.Is(err, os.ErrNotExist), "temporary file left: %v", err)
}
