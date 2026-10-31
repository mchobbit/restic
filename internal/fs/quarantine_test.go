package fs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
)

func TestNodeUnreadQuarantined(t *testing.T) {
	node := &data.Node{Type: data.NodeTypeFile, Size: 4096}
	node.SetSparse()
	err := NodeUnreadQuarantined(node)
	var w *BackupWarning
	rtest.Assert(t, errors.As(err, &w), "expected a *BackupWarning, got %v", err)
	rtest.Equals(t, "av_quarantined: contents not backed up (4096 bytes)", err.Error())
	rest, warnings := SplitBackupWarnings(err)
	rtest.Assert(t, rest == nil && len(warnings) == 1, "not a backup warning alone: %v %v", rest, warnings)

	rtest.Equals(t, uint64(0), node.Size)
	rtest.Assert(t, node.Content != nil && len(node.Content) == 0, "content %v, want empty", node.Content)
	size, ok, err := node.UnreadSize()
	rtest.OK(t, err)
	rtest.Assert(t, ok && size == 4096, "unread size %v %v, want 4096", size, ok)
	sparse, err := node.Sparse()
	rtest.OK(t, err)
	rtest.Assert(t, !sparse, "a node without contents marked sparse")
}

func TestQuarantinedOpenError(t *testing.T) {
	// An ordinary file isn't quarantined, whatever the error; and only
	// EACCES is av_quarantined's.
	path := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(path, []byte("x"), 0o600))
	for _, err := range []error{
		&os.PathError{Op: "open", Path: path, Err: syscall.EACCES},
		&os.PathError{Op: "open", Path: path, Err: syscall.EPERM},
		&os.PathError{Op: "open", Path: path, Err: syscall.ENOENT},
	} {
		rtest.Assert(t, !QuarantinedOpenError(path, err), "%v taken for av_quarantined", err)
	}
}
