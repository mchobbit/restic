package fs

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

func TestBcachefsOption(t *testing.T) {
	for name, want := range map[string]bool{
		"bcachefs.compression":           true,
		"bcachefs.background_target":     true,
		"bcachefs.project":               false,
		"bcachefs_effective.compression": false,
		"user.bcachefs.compression":      false,
	} {
		rtest.Equals(t, want, bcachefsOption(name))
	}
}

// Off bcachefs a directory gets nothing early, and a file without a project
// ID isn't opened for one.
func TestPrepareDirForOptionsNotBcachefs(t *testing.T) {
	d := t.TempDir()
	if isBcachefsPath(d) {
		t.Skip("the test directory is on bcachefs")
	}
	node := &data.Node{Type: data.NodeTypeDir, ExtendedAttributes: []data.ExtendedAttribute{
		{Name: "user.backotter", Value: []byte("x")},
	}}
	PrepareDirForOptions(d, node, func(string) bool { return true })
	names, err := listxattr(d)
	rtest.OK(t, err)
	rtest.Assert(t, !slices.Contains(names, "user.backotter"), "user.backotter set early off bcachefs")

	f := filepath.Join(d, "f")
	rtest.OK(t, os.WriteFile(f, nil, 0o000))
	if os.Geteuid() != 0 {
		// Not readable: opening it would fail.
		rtest.OK(t, nodeRestoreOSAttributes(&data.Node{Type: data.NodeTypeFile}, f))
	}
}

// mountBcachefs formats a bcachefs image (mkfs.bcachefs, or the command
// $BKO_MKFS_BCACHEFS names, given the image last) and mounts it, for root.
func mountBcachefs(t *testing.T) string {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to mount a file system")
	}
	mkfs := strings.Fields(os.Getenv("BKO_MKFS_BCACHEFS"))
	if len(mkfs) == 0 {
		if _, err := exec.LookPath("mkfs.bcachefs"); err != nil {
			t.Skip("no mkfs.bcachefs, nor $BKO_MKFS_BCACHEFS")
		}
		mkfs = []string{"mkfs.bcachefs"}
	}
	tmp := t.TempDir()
	img := filepath.Join(tmp, "bcachefs.img")
	mnt := filepath.Join(tmp, "mnt")
	rtest.OK(t, os.Mkdir(mnt, 0o755))
	rtest.OK(t, os.WriteFile(img, nil, 0o600))
	rtest.OK(t, os.Truncate(img, 256<<20))
	if out, err := exec.Command(mkfs[0], append(mkfs[1:], "-f", img)...).CombinedOutput(); err != nil {
		t.Skipf("%v: %v: %s", mkfs, err, out)
	}
	if out, err := exec.Command("mount", "-t", "bcachefs", "-o", "loop", img, mnt).CombinedOutput(); err != nil {
		t.Skipf("mount: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", mnt).Run() })
	return mnt
}

func effectiveProject(t *testing.T, path string) uint32 {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	var fsx fsxattr
	rtest.OK(t, ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx))
	return fileProjectID(fd, fsx.projid, true)
}

// On bcachefs a directory's options and project ID go on before its entries,
// which inherit them; an entry whose project ID is its directory's isn't
// given it again (it stays inherited, with no bcachefs.project of its own),
// one without a project ID gets 0, and a snapshot's bcachefs.project is
// neither set nor a reason for an error.
func TestBcachefsDirOptionsInherited(t *testing.T) {
	mnt := mountBcachefs(t)
	d := filepath.Join(mnt, "d")
	rtest.OK(t, os.Mkdir(d, 0o755))
	dir := &data.Node{Type: data.NodeTypeDir, ExtendedAttributes: []data.ExtendedAttribute{
		{Name: "bcachefs.compression", Value: []byte("zstd")},
		{Name: "bcachefs.project", Value: []byte("43")}, // an older snapshot's
	}}
	dir.SetProjectID(43)
	PrepareDirForOptions(d, dir, func(string) bool { return true })
	rtest.Equals(t, uint32(43), effectiveProject(t, d))

	kid := filepath.Join(d, "kid")
	rtest.OK(t, os.WriteFile(kid, []byte("data"), 0o644))
	zero := filepath.Join(d, "zero")
	rtest.OK(t, os.WriteFile(zero, []byte("data"), 0o644))
	compression, err := getxattr(kid, "bcachefs_effective.compression")
	rtest.OK(t, err)
	rtest.Assert(t, strings.HasPrefix(string(compression), "zstd"), "kid's compression is %q", compression)

	kidNode := &data.Node{Type: data.NodeTypeFile}
	kidNode.SetProjectID(43)
	rtest.OK(t, nodeRestoreOSAttributes(kidNode, kid))
	rtest.OK(t, nodeRestoreOSAttributes(&data.Node{Type: data.NodeTypeFile}, zero))
	rtest.OK(t, nodeRestoreOSAttributes(dir, d))
	rtest.OK(t, nodeRestoreExtendedAttributes(dir, d, func(string) bool { return true }))

	rtest.Equals(t, uint32(43), effectiveProject(t, kid))
	rtest.Equals(t, uint32(0), effectiveProject(t, zero))
	names, err := listxattr(kid)
	rtest.OK(t, err)
	rtest.Assert(t, !slices.Contains(names, "bcachefs.project"), "kid has a project ID of its own: %v", names)
	rtest.Assert(t, !slices.Contains(names, "bcachefs.compression"), "kid has a compression of its own: %v", names)
	own, err := getxattr(d, "bcachefs.compression")
	rtest.OK(t, err)
	rtest.Equals(t, "zstd", strings.TrimRight(string(own), "\x00"))
}
