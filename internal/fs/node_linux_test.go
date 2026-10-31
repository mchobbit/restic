package fs

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	rtest "github.com/restic/restic/internal/test"
)

func TestRestoreSymlinkTimestampsError(t *testing.T) {
	d := t.TempDir()
	node := data.Node{Type: data.NodeTypeSymlink}
	err := nodeRestoreTimestamps(&node, d+"/nosuchfile")
	rtest.Assert(t, errors.Is(err, fs.ErrNotExist), "want ErrNotExist, got %q", err)
	rtest.Assert(t, strings.Contains(err.Error(), d), "filename not in %q", err)
}

// TestNodeRestoreMetadataNotRoot restores, not as root, a node owned by root
// with a trusted.* extended attribute: lchown's EPERM is ignored, but the
// trusted.* attribute's is a warning naming it, which doesn't keep the
// user.* attribute from being set.
func TestNodeRestoreMetadataNotRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	path := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(path, nil, 0o600))
	now := time.Now()
	node := &data.Node{
		Type: data.NodeTypeFile, Mode: 0o644, UID: 0, GID: 0, ModTime: now, AccessTime: now,
		ExtendedAttributes: []data.ExtendedAttribute{
			{Name: "trusted.bko", Value: []byte("t")},
			{Name: "user.bko", Value: []byte("u")},
		},
	}
	err := NodeRestoreMetadata(node, path, func(msg string) { t.Errorf("warning: %v", msg) },
		func(string) bool { return true }, false)
	rest, warnings := SplitWarnings(err)
	rtest.OK(t, rest)
	rtest.Equals(t, 1, len(warnings))
	var notRoot *NotRootWarning
	rtest.Assert(t, errors.As(warnings[0], &notRoot), "not a NotRootWarning: %v", warnings[0])
	rtest.Equals(t, path, notRoot.Path)
	rtest.Equals(t, 1, len(notRoot.Errs))
	rtest.Assert(t, strings.Contains(notRoot.Error(), "trusted.bko"), "trusted.bko not named in %q", notRoot)
	rtest.Assert(t, !strings.Contains(notRoot.Error(), "user.bko"), "user.bko named in %q", notRoot)

	if value, err := getxattr(path, "user.bko"); err == nil && value != nil {
		rtest.Equals(t, []byte("u"), value)
	}
}
