//go:build !windows

package restorer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
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
