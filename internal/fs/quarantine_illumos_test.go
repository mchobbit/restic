//go:build illumos

package fs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
)

// A file with av_quarantined, which root can't open either: its system
// attributes are read without opening it, and its node is kept.  Needs root
// and ZFS (TMPDIR on a ZFS file system; /tmp is tmpfs).
func TestQuarantinedFile(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	path := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(path, []byte("hello"), 0o644))
	if out, err := exec.Command("/usr/bin/chmod", "S+vav_quarantined", path).CombinedOutput(); err != nil {
		t.Skipf("can't set av_quarantined (not ZFS?): %v %s", err, out)
	}

	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		t.Fatal("a quarantined file opened")
	}
	rtest.Assert(t, errors.Is(err, syscall.EACCES), "open: %v, want EACCES", err)
	rtest.Assert(t, QuarantinedOpenError(path, err), "%v not taken for av_quarantined", err)
	rtest.Assert(t, !QuarantinedOpenError(path, &os.PathError{Op: "open", Path: path, Err: syscall.EPERM}),
		"EPERM taken for av_quarantined")

	fi, err := os.Lstat(path)
	rtest.OK(t, err)
	node, err := nodeFromFileInfo(path, ExtendedStat(fi), true, func(string, ...any) {})
	rtest.OK(t, err)
	flags, ok, err := node.FileFlags(data.TypeIllumosSystemAttributes)
	rtest.OK(t, err)
	rtest.Assert(t, ok && flags&illumosSysattrBits["av_quarantined"] != 0,
		"system attributes %#x (%v), want av_quarantined", flags, ok)
	_, ok, err = node.BirthTime()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "no creation time")

	// Writing them works the same way: nodump on, then the attributes as
	// they were.
	nodump := illumosSysattrBits["nodump"]
	rtest.OK(t, restoreFileFlags(path, data.NodeTypeFile, flags|nodump, nil))
	rtest.Assert(t, isQuarantined(path), "av_quarantined lost")
	rtest.OK(t, restoreFileFlags(path, data.NodeTypeFile, flags, nil))

	ordinary := filepath.Join(filepath.Dir(path), "ordinary")
	rtest.OK(t, os.WriteFile(ordinary, nil, 0o644))
	rtest.Assert(t, !isQuarantined(ordinary), "an ordinary file taken for quarantined")
}
