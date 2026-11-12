//go:build linux

package fs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

func TestFATAttributesIoctls(t *testing.T) {
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "arm", "riscv64", "loong64", "s390x":
		rtest.Equals(t, uint(0x80047210), fatIOCGetAttributes)
		rtest.Equals(t, uint(0x40047211), fatIOCSetAttributes)
	}
}

func TestKeptFATAttributes(t *testing.T) {
	for _, c := range []struct {
		attrs uint32
		dir   bool
		kept  uint32
		ok    bool
	}{
		{0x20, false, 0, false},    // a new or written file: archive alone
		{0x21, false, 0, false},    // read-only too, which its mode says
		{0x30, true, 0x20, true},   // a directory with archive (made elsewhere)
		{0x10, true, 0, false},     // a new directory: none but ATTR_DIR
		{0x00, false, 0, true},     // archive cleared
		{0x22, false, 0x22, true},  // hidden
		{0x24, false, 0x24, true},  // system
		{0x06, false, 0x06, true},  // hidden, system, archive cleared
		{0x01, false, 0x01, true},  // read-only, archive cleared
		{0x12, true, 0x02, true},   // a hidden directory
		{0x11, true, 0x01, true},   // a read-only directory
		{0x28, false, 0x20, false}, // volume label: never kept
	} {
		kept, ok := keptFATAttributes(c.attrs, c.dir)
		rtest.Assert(t, ok == c.ok && (!ok || kept == c.kept), "%#x dir %v: got %#x %v, want %#x %v", c.attrs, c.dir, kept, ok, c.kept, c.ok)
	}
	rtest.Equals(t, uint32(0x16), mergeFATAttributes(0x30, 0x06))
	rtest.Equals(t, uint32(0x08), mergeFATAttributes(0x2f, 0))
}

func TestRestoreFATAttributesWith(t *testing.T) {
	var sets []uint32
	set := func(a uint32) error { sets = append(sets, a); return nil }
	get := func(a uint32) func() (uint32, error) { return func() (uint32, error) { return a, nil } }

	// Right already: no set.
	rtest.OK(t, restoreFATAttributesWith("f", 0x22, get(0x22), set))
	rtest.Equals(t, 0, len(sets))
	// The kept ones replaced, ATTR_DIR left.
	rtest.OK(t, restoreFATAttributesWith("d", 0x02, get(0x10), set))
	rtest.Equals(t, []uint32{0x12}, sets)
	// The root directory takes none: EINVAL is unsupported.
	err := restoreFATAttributesWith("/", 0x02, get(0x10), func(uint32) error { return unix.EINVAL })
	var unsupported *UnsupportedError
	rtest.Assert(t, errors.As(err, &unsupported) && unsupported.Attr == data.TypeLinuxFATAttributes, "want unsupported, got %v", err)
	// EPERM is an error.
	err = restoreFATAttributesWith("f", 0x02, get(0x20), func(uint32) error { return unix.EPERM })
	rtest.Assert(t, err != nil && !errors.As(err, &unsupported), "want an error, got %v", err)
}

func TestKeptXFSOptions(t *testing.T) {
	_, ok := keptXFSOptions(fsxattr{xflags: 0x80000002 | 0x8 | 0x200}, false) // hasattr, prealloc, immutable, projinherit
	rtest.Assert(t, !ok, "no XFS options kept")
	opts, ok := keptXFSOptions(fsxattr{xflags: 0x80000802, extsize: 1 << 20}, false)
	rtest.Assert(t, ok, "extsize not kept")
	rtest.Equals(t, data.XFSOptions{XFlags: 0x800, ExtSize: 1 << 20}, opts)
	opts, ok = keptXFSOptions(fsxattr{xflags: 0x1000 | 0x4000 | 0x10000, extsize: 2 << 20, cowextsize: 4 << 20}, true)
	rtest.Assert(t, ok, "extsz-inherit not kept")
	rtest.Equals(t, data.XFSOptions{XFlags: 0x15000, ExtSize: 2 << 20, CowExtSize: 4 << 20}, opts)
	opts, _ = keptXFSOptions(fsxattr{xflags: 0x2000, extsize: 4096}, false)
	rtest.Equals(t, data.XFSOptions{XFlags: 0x2000}, opts)

	// The kept flags and sizes replaced; prealloc, immutable and the
	// project ID left as they are.
	cur := fsxattr{xflags: 0x80000002 | 0x8 | 0x2000, projid: 7}
	want := wantedFsxattr(cur, data.XFSOptions{XFlags: 0x800, ExtSize: 1 << 20}, false)
	rtest.Equals(t, fsxattr{xflags: 0x80000002 | 0x8 | 0x800, extsize: 1 << 20, projid: 7}, want)
	rtest.Assert(t, !sameXFSOptions(cur, want), "different options taken for the same")
	want = wantedFsxattr(fsxattr{xflags: 0x800, extsize: 1 << 20}, data.XFSOptions{}, false)
	rtest.Equals(t, fsxattr{}, want)

	// Realtime inheritance: kept on a directory, alone too, and set and
	// cleared there; on a regular file neither kept nor set.  Realtime
	// itself never.
	opts, ok = keptXFSOptions(fsxattr{xflags: 0x80000000 | 0x100}, true)
	rtest.Assert(t, ok, "a directory's rtinherit not kept")
	rtest.Equals(t, data.XFSOptions{XFlags: 0x100}, opts)
	_, ok = keptXFSOptions(fsxattr{xflags: 0x100}, false)
	rtest.Assert(t, !ok, "a regular file's rtinherit kept")
	_, ok = keptXFSOptions(fsxattr{xflags: 0x1}, false)
	rtest.Assert(t, !ok, "realtime kept")
	opts, _ = keptXFSOptions(fsxattr{xflags: 0x1 | 0x100 | 0x2000}, true)
	rtest.Equals(t, data.XFSOptions{XFlags: 0x2100}, opts)
	rtest.Equals(t, fsxattr{xflags: 0x2100}, wantedFsxattr(fsxattr{xflags: 0x2000}, data.XFSOptions{XFlags: 0x2100}, true))
	rtest.Equals(t, fsxattr{xflags: 0x2000}, wantedFsxattr(fsxattr{xflags: 0x2100}, data.XFSOptions{XFlags: 0x2000}, true))
	rtest.Equals(t, fsxattr{xflags: 0x2000}, wantedFsxattr(fsxattr{xflags: 0x2000}, data.XFSOptions{XFlags: 0x2101}, false))
	rtest.Equals(t, fsxattr{}, wantedFsxattr(fsxattr{}, data.XFSOptions{XFlags: 0x1}, true))
	rtest.Assert(t, sameXFSOptions(fsxattr{xflags: 0x2000, nextents: 3}, wantedFsxattr(fsxattr{xflags: 0x2000, nextents: 3}, data.XFSOptions{XFlags: 0x2000}, false)), "same options differ")
}

// FAT attributes and XFS options restored onto another file system are
// unsupported there.
func TestRestoreFATXFSNotThere(t *testing.T) {
	dir := rtest.TempDir(t)
	var st unix.Statfs_t
	rtest.OK(t, unix.Statfs(dir, &st))
	if uint32(st.Type) == msdosSuperMagic || uint32(st.Type) == xfsSuperMagic {
		t.Skip("the temporary directory is on FAT or XFS")
	}
	path := filepath.Join(dir, "f")
	rtest.OK(t, os.WriteFile(path, []byte("x"), 0o600))
	var unsupported *UnsupportedError
	err := RestoreFATAttributes(path, data.NodeTypeFile, 0x22)
	rtest.Assert(t, errors.As(err, &unsupported) && errors.Is(err, errNotFAT), "want not FAT, got %v", err)
	err = RestoreXFSOptions(dir, data.NodeTypeDir, data.XFSOptions{XFlags: 0x2000})
	rtest.Assert(t, errors.As(err, &unsupported) && errors.Is(err, errNotXFS), "want not XFS, got %v", err)
	// Early, nothing happens: the file is left as it is.
	PrepareFileForXFSOptions(path, data.XFSOptions{XFlags: 0x800, ExtSize: 1 << 20})
	got, err := os.ReadFile(path)
	rtest.OK(t, err)
	rtest.Equals(t, "x", string(got))
}

// mountLoop makes a file system with mkfs (and args) on an image of size
// bytes and mounts it, for the test's lifetime.  It skips the test without
// root or the tools.
func mountLoop(t *testing.T, size int64, mkfs string, args ...string) string {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to mount a file system")
	}
	if _, err := exec.LookPath(mkfs); err != nil {
		t.Skipf("no %v", mkfs)
	}
	tmp := rtest.TempDir(t)
	img := filepath.Join(tmp, "fs.img")
	mnt := filepath.Join(tmp, "mnt")
	rtest.OK(t, os.Mkdir(mnt, 0o755))
	rtest.OK(t, os.WriteFile(img, nil, 0o600))
	rtest.OK(t, os.Truncate(img, size))
	if out, err := exec.Command(mkfs, append(args, img)...).CombinedOutput(); err != nil {
		t.Skipf("%v: %v: %s", mkfs, err, out)
	}
	if out, err := exec.Command("mount", "-o", "loop", img, mnt).CombinedOutput(); err != nil {
		t.Skipf("mount: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", mnt).Run() })
	// A loop device another test used has the same device number: the
	// file system types cached for the backup are forgotten.
	fsTypes.Range(func(dev, _ any) bool { fsTypes.Delete(dev); return true })
	return mnt
}

// fillNode is the node nodeFillFileFlags makes of the file at path.
func fillNode(t *testing.T, path string, typ data.NodeType) *data.Node {
	var st unix.Stat_t
	rtest.OK(t, unix.Lstat(path, &st))
	node := &data.Node{Type: typ}
	rtest.OK(t, nodeFillFileFlags(node, path, &ExtendedFileInfo{DeviceID: uint64(st.Dev), Inode: st.Ino, Size: st.Size, Blocks: st.Blocks}))
	return node
}

// With root, on a loop vfat: the attributes a backup keeps, and a restore
// putting them back.
func TestFATAttributesOnVfat(t *testing.T) {
	mnt := mountLoop(t, 64<<20, "mkfs.vfat")
	getAttrs := func(path string) uint32 {
		fd, err := unix.Open(path, unix.O_RDONLY, 0)
		rtest.OK(t, err)
		defer func() { _ = unix.Close(fd) }()
		var a uint32
		rtest.OK(t, ioctlUint32(fd, fatIOCGetAttributes, &a))
		return a
	}
	setAttrs := func(path string, a uint32) {
		fd, err := unix.Open(path, unix.O_RDONLY, 0)
		rtest.OK(t, err)
		defer func() { _ = unix.Close(fd) }()
		rtest.OK(t, ioctlUint32(fd, fatIOCSetAttributes, &a))
	}
	for name, c := range map[string]struct {
		dir   bool
		attrs uint32
		kept  bool
	}{
		"plain": {false, 0x20, false}, "hidden": {false, 0x22, true}, "cleared": {false, 0, true},
		"system": {false, 0x24, true}, "pdir": {true, 0x10, false}, "hdir": {true, 0x12, true}, "rodir": {true, 0x11, true},
	} {
		src, dst := filepath.Join(mnt, name), filepath.Join(mnt, "restored-"+name)
		typ := data.NodeTypeFile
		if c.dir {
			typ = data.NodeTypeDir
			rtest.OK(t, os.Mkdir(src, 0o755))
			rtest.OK(t, os.Mkdir(dst, 0o755))
		} else {
			rtest.OK(t, os.WriteFile(src, []byte(name), 0o644))
			rtest.OK(t, os.WriteFile(dst, []byte(name), 0o644))
		}
		setAttrs(src, c.attrs)
		node := fillNode(t, src, typ)
		attrs, ok, err := node.FATAttributes()
		rtest.OK(t, err)
		rtest.Assert(t, ok == c.kept, "%v: kept %v, want %v (%#x)", name, ok, c.kept, attrs)
		if !ok {
			continue
		}
		rtest.OK(t, RestoreFATAttributes(dst, typ, attrs))
		rtest.Equals(t, getAttrs(src), getAttrs(dst))
	}
	// The root directory: no attribute, and a node's for it is unsupported.
	node := fillNode(t, mnt, data.NodeTypeDir)
	_, ok, _ := node.FATAttributes()
	rtest.Assert(t, !ok, "the root directory has FAT attributes")
	var unsupported *UnsupportedError
	err := RestoreFATAttributes(mnt, data.NodeTypeDir, 0x02)
	rtest.Assert(t, errors.As(err, &unsupported), "want unsupported on the root, got %v", err)
}

// With root, on a loop XFS: the options a backup keeps, set early on an
// empty file (an extent size hint) and late on a directory.
func TestXFSOptionsOnXFS(t *testing.T) {
	mnt := mountLoop(t, 320<<20, "mkfs.xfs", "-q", "-f")
	xfsIO := func(args ...string) {
		if out, err := exec.Command("xfs_io", args...).CombinedOutput(); err != nil {
			t.Fatalf("xfs_io %v: %v: %s", args, err, out)
		}
	}
	file, dir := filepath.Join(mnt, "extsize"), filepath.Join(mnt, "dir")
	rtest.OK(t, os.WriteFile(file, nil, 0o644))
	rtest.OK(t, os.Mkdir(dir, 0o755))
	xfsIO("-c", "extsize 1m", "-c", "chattr +f", file)
	xfsIO("-c", "extsize 2m", "-c", "chattr +S", "-c", "chattr +t", dir)
	rtest.OK(t, os.WriteFile(file, make([]byte, 65536), 0o644))

	fopts, ok, err := fillNode(t, file, data.NodeTypeFile).XFSOptions()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "file's XFS options not kept")
	rtest.Equals(t, data.XFSOptions{XFlags: 0x2800, ExtSize: 1 << 20}, fopts)
	dopts, ok, err := fillNode(t, dir, data.NodeTypeDir).XFSOptions()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "directory's XFS options not kept")
	rtest.Equals(t, data.XFSOptions{XFlags: 0x5100, ExtSize: 2 << 20}, dopts)
	plain := filepath.Join(mnt, "plain")
	f, err := os.Create(plain)
	rtest.OK(t, err)
	_, err = f.Write([]byte("x"))
	rtest.OK(t, err)
	rtest.OK(t, f.Sync()) // its extent allocated, not delayed
	rtest.OK(t, f.Close())
	_, ok, _ = fillNode(t, plain, data.NodeTypeFile).XFSOptions()
	rtest.Assert(t, !ok, "a plain file has XFS options")

	// Early on a new file, then its data, then late: as the restorer does.
	rfile, rdir := filepath.Join(mnt, "rfile"), filepath.Join(mnt, "rdir")
	PrepareFileForXFSOptions(rfile, fopts)
	rtest.OK(t, os.WriteFile(rfile, make([]byte, 65536), 0o644))
	rtest.OK(t, RestoreXFSOptions(rfile, data.NodeTypeFile, fopts))
	got, _, _ := fillNode(t, rfile, data.NodeTypeFile).XFSOptions()
	rtest.Equals(t, fopts, got)
	rtest.OK(t, os.Mkdir(rdir, 0o755))
	rtest.OK(t, RestoreXFSOptions(rdir, data.NodeTypeDir, dopts))
	got, _, _ = fillNode(t, rdir, data.NodeTypeDir).XFSOptions()
	rtest.Equals(t, dopts, got)
	// A regular file is given no realtime inheritance (XFS has none
	// for it).
	rtest.OK(t, RestoreXFSOptions(rfile, data.NodeTypeFile, data.XFSOptions{XFlags: fopts.XFlags | 0x100, ExtSize: fopts.ExtSize}))
	got, _, _ = fillNode(t, rfile, data.NodeTypeFile).XFSOptions()
	rtest.Equals(t, fopts, got)
	// Too late for a file with data: unsupported (EINVAL).
	err = RestoreXFSOptions(plain, data.NodeTypeFile, fopts)
	var unsupported *UnsupportedError
	rtest.Assert(t, errors.As(err, &unsupported), "want unsupported for a file with data, got %v", err)
	// Taken off again.
	rtest.OK(t, RestoreXFSOptions(rdir, data.NodeTypeDir, data.XFSOptions{}))
	_, ok, _ = fillNode(t, rdir, data.NodeTypeDir).XFSOptions()
	rtest.Assert(t, !ok, "XFS options not cleared")
}
