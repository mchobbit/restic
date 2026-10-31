//go:build !windows

package restorer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

// A file backed up with holes (unix.sparse) is only written sparse into a
// new file.  Over an existing regular file it is written in full, whatever
// that file was, so that no hole leaves old bytes in place; over a symlink or
// a directory, which the restorer replaces, it is written sparse.
func TestRestorerSparseMarkerExistingTarget(t *testing.T) {
	zeros := string(make([]byte, chunker.MinSize))
	content := "start" + zeros + zeros + "end"
	markSparse := func(_ *FileAttributes, _ bool) map[data.GenericAttributeType]json.RawMessage {
		return map[data.GenericAttributeType]json.RawMessage{data.TypeUnixSparse: json.RawMessage(`true`)}
	}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"file": File{DataParts: []string{"start", zeros, zeros, "end"}},
		},
	}, markSparse)
	old := bytes.Repeat([]byte{0xaa}, len(content)+4096)

	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, target string)
		root    bool // the test needs a user other than root
		opts    Options
	}{
		{"regular file", func(t *testing.T, target string) {
			rtest.OK(t, os.WriteFile(target, old, 0o644))
		}, false, Options{}},
		{"unreadable regular file", func(t *testing.T, target string) {
			rtest.OK(t, os.WriteFile(target, old, 0o644))
			rtest.OK(t, os.Chmod(target, 0))
		}, true, Options{}},
		{"symlink", func(t *testing.T, target string) {
			rtest.OK(t, os.WriteFile(target+".old", old, 0o644))
			rtest.OK(t, os.Symlink(filepath.Base(target)+".old", target))
		}, false, Options{}},
		{"directory, --delete", func(t *testing.T, target string) {
			rtest.OK(t, os.Mkdir(target, 0o755))
			rtest.OK(t, os.WriteFile(filepath.Join(target, "entry"), old, 0o644))
		}, false, Options{Delete: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.root && os.Geteuid() == 0 {
				t.Skip("root reads every file")
			}
			dst := rtest.TempDir(t)
			target := filepath.Join(dst, "file")
			test.prepare(t, target)

			res := NewRestorer(repo, sn, test.opts)
			_, err := res.RestoreTo(context.TODO(), dst)
			rtest.OK(t, err)
			restored, err := os.ReadFile(target)
			rtest.OK(t, err)
			rtest.Assert(t, string(restored) == content, "wrong content restored over a %v", test.name)
		})
	}
}

// A file restored sparse gets every all-zero block of the file system wholly
// inside one of its blobs as a hole, not only each blob's leading zeros: it
// takes no more blocks than the blocks its blobs' data touch, give or take
// the file system's own.
func TestRestorerSparseBlocks(t *testing.T) {
	const mib = 1 << 20
	part := func(n int, data map[int]string) string {
		p := make([]byte, n)
		for off, s := range data {
			copy(p[off:], s)
		}
		return string(p)
	}
	parts := []string{
		part(5000, map[int]string{0: "start"}),
		part(mib+13, map[int]string{300000: "in the middle"}),
		part(chunker.MinSize, nil),
		part(2*mib, map[int]string{2*mib - 1: "!"}),
		part(777777, map[int]string{0: "head", 777777 - 4: "tail"}),
	}
	content := strings.Join(parts, "")
	markSparse := func(_ *FileAttributes, _ bool) map[data.GenericAttributeType]json.RawMessage {
		return map[data.GenericAttributeType]json.RawMessage{data.TypeUnixSparse: json.RawMessage(`true`)}
	}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{"file": File{DataParts: parts}},
	}, markSparse)

	dst := rtest.TempDir(t)
	res := NewRestorer(repo, sn, Options{})
	_, err := res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)
	path := filepath.Join(dst, "file")
	restored, err := os.ReadFile(path)
	rtest.OK(t, err)
	rtest.Assert(t, string(restored) == content, "wrong content restored")

	f, err := os.Open(path)
	rtest.OK(t, err)
	bs := fs.HoleBlockSize(f)
	rtest.OK(t, f.Close())
	// the blocks writeSparse writes into
	touched := map[int64]bool{}
	var offset int64
	for _, p := range parts {
		_, err := writeSparse(func(q []byte, off int64) (int, error) {
			for b := off / bs; b <= (off+int64(len(q))-1)/bs; b++ {
				touched[b] = true
			}
			return len(q), nil
		}, []byte(p), offset, bs)
		rtest.OK(t, err)
		offset += int64(len(p))
	}
	want := int64(len(touched)) * bs
	got := getBlockCount(t, path) * 512
	t.Logf("%d bytes restored in %d bytes of blocks, %d blocks of %d touched (%d bytes)", len(content), got, len(touched), bs, want)

	probe := filepath.Join(dst, "probe")
	rtest.OK(t, os.WriteFile(probe, nil, 0o600))
	rtest.OK(t, os.Truncate(probe, mib))
	if getBlockCount(t, probe) != 0 {
		return // a file system without holes
	}
	// some slack for the file system's own blocks (extent trees, ...)
	rtest.Assert(t, got <= want+64<<10, "%d bytes allocated, more than the %d written", got, want)
}
