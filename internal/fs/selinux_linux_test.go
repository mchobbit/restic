package fs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

// RemoveSELinuxLabel removes a label, and leaves the other attributes and a
// file without one alone.
func TestRemoveSELinuxLabel(t *testing.T) {
	dir := rtest.TempDir(t)
	labelled, plain := filepath.Join(dir, "labelled"), filepath.Join(dir, "plain")
	rtest.OK(t, os.WriteFile(labelled, nil, 0o644))
	rtest.OK(t, os.WriteFile(plain, nil, 0o644))
	if err := setxattr(labelled, selinuxLabel, []byte("system_u:object_r:etc_t:s0")); err != nil {
		t.Skipf("no SELinux label can be set here: %v", err)
	}
	if err := setxattr(labelled, "user.color", []byte("blue")); err != nil {
		t.Skipf("no user xattrs here: %v", err)
	}

	rtest.OK(t, RemoveSELinuxLabel(labelled))
	rtest.OK(t, RemoveSELinuxLabel(plain))
	names, err := listxattr(labelled)
	rtest.OK(t, err)
	rtest.Assert(t, !slices.Contains(names, selinuxLabel), "label kept: %v", names)
	rtest.Assert(t, slices.Contains(names, "user.color"), "user.color removed: %v", names)
}
