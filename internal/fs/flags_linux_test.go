//go:build linux

package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

func TestFsxattrIoctls(t *testing.T) {
	rtest.Equals(t, uintptr(28), unsafe.Sizeof(fsxattr{}))
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "arm", "riscv64", "loong64", "s390x":
		rtest.Equals(t, uint(0x801c581f), fsIOCFSGetXattr)
		rtest.Equals(t, uint(0x401c5820), fsIOCFSSetXattr)
	case "mips", "mipsle", "mips64", "mips64le", "ppc64", "ppc64le":
		rtest.Equals(t, uint(0x401c581f), fsIOCFSGetXattr)
		rtest.Equals(t, uint(0x801c5820), fsIOCFSSetXattr)
	}
}

func TestMergeInodeFlags(t *testing.T) {
	const (
		immutable = 0x00000010 // FS_IMMUTABLE_FL
		nocow     = 0x00800000 // FS_NOCOW_FL
		casefold  = 0x40000000 // FS_CASEFOLD_FL
		extents   = 0x00080000 // FS_EXTENT_FL, the file system's own
	)
	// The file system's own flags stay.
	rtest.Equals(t, uint32(extents|casefold|immutable), mergeInodeFlags(extents|casefold, casefold|immutable))
	rtest.Equals(t, uint32(extents), mergeInodeFlags(extents|nocow, extents))
	// Casefolding stays as the file has it, both ways: only an empty
	// directory changes it (prepareDirForFlags).
	rtest.Equals(t, uint32(extents|casefold), mergeInodeFlags(extents|casefold, 0))
	rtest.Equals(t, uint32(extents|immutable), mergeInodeFlags(extents, casefold|immutable))
	// Flags that are right already need no FS_IOC_SETFLAGS.
	rtest.Equals(t, uint32(extents|nocow), mergeInodeFlags(extents|nocow, nocow))
}

// fakeInodeFlags is a file's inode flags for restoreFlagsWith, recording
// the calls made.
type fakeInodeFlags struct {
	flags, xflags uint32
	calls         []string
	refuse        uint32 // flags SETFLAGS refuses with EOPNOTSUPP
}

func (f *fakeInodeFlags) ops(compression bool) inodeFlagOps {
	ops := inodeFlagOps{
		get: func() (uint32, error) {
			return f.flags, nil
		},
		set: func(flags uint32) error {
			f.calls = append(f.calls, fmt.Sprintf("SETFLAGS %#x", flags))
			if flags&f.refuse != 0 {
				return unix.EOPNOTSUPP
			}
			f.flags = flags
			return nil
		},
		addXflags: func(xflags uint32) error {
			f.calls = append(f.calls, fmt.Sprintf("FSSETXATTR +%#x", xflags))
			f.xflags |= xflags
			if xflags&fsXflagImmutable != 0 {
				f.flags |= fsImmutableFl
			}
			if xflags&fsXflagAppend != 0 {
				f.flags |= fsAppendFl
			}
			return nil
		},
	}
	if compression {
		ops.setCompression = func() error {
			f.calls = append(f.calls, "setxattr btrfs.compression")
			return nil
		}
	}
	return ops
}

func TestRestoreFlagsWith(t *testing.T) {
	const (
		compr     = 0x00000004 // FS_COMPR_FL
		immutable = 0x00000010 // FS_IMMUTABLE_FL
		nodump    = 0x00000040 // FS_NODUMP_FL
		extents   = 0x00080000 // FS_EXTENT_FL
		casefold  = 0x40000000 // FS_CASEFOLD_FL
		badBit    = 0x00000080 // FS_NOATIME_FL, refused below
	)

	// Unchanged: no SETFLAGS at all (btrfs would rewrite its compression
	// property for nothing).
	f := &fakeInodeFlags{flags: extents | nodump}
	rtest.OK(t, restoreFlagsWith("file", data.NodeTypeFile, nodump, f.ops(false)))
	rtest.Equals(t, []string(nil), f.calls)

	// Changed: one SETFLAGS, keeping the file system's own flags.
	f = &fakeInodeFlags{flags: extents}
	rtest.OK(t, restoreFlagsWith("file", data.NodeTypeFile, nodump|immutable, f.ops(false)))
	rtest.Equals(t, []string{"SETFLAGS 0x80050"}, f.calls)

	// Casefolding is never set late, and a directory whose casefolding
	// differs from its node's gets a warning, both ways.
	for _, test := range []struct{ current, flags uint32 }{{0, casefold}, {casefold, 0}} {
		f = &fakeInodeFlags{flags: test.current}
		err := restoreFlagsWith("dir", data.NodeTypeDir, test.flags|nodump, f.ops(false))
		var warning *FlagsWarning
		rtest.Assert(t, errors.As(err, &warning), "no casefolding warning for %#x on %#x: %v", test.flags, test.current, err)
		rtest.Assert(t, IsWarning(err), "casefolding not restored is an error: %v", err)
		rtest.Equals(t, test.current|nodump, f.flags)
	}
	f = &fakeInodeFlags{flags: casefold}
	rtest.OK(t, restoreFlagsWith("dir", data.NodeTypeDir, casefold|nodump, f.ops(false)))

	// btrfs.compression: the flags but i and a first, the property again,
	// then i through FS_IOC_FSSETXATTR, which leaves the property alone.
	f = &fakeInodeFlags{flags: compr}
	rtest.OK(t, restoreFlagsWith("file", data.NodeTypeFile, compr|nodump|immutable, f.ops(true)))
	rtest.Equals(t, []string{"SETFLAGS 0x44", "setxattr btrfs.compression", "FSSETXATTR +0x8"}, f.calls)
	rtest.Equals(t, uint32(compr|nodump|immutable), f.flags)
	// Only i to add: no SETFLAGS, no property.
	f = &fakeInodeFlags{flags: compr}
	rtest.OK(t, restoreFlagsWith("file", data.NodeTypeFile, compr|immutable, f.ops(true)))
	rtest.Equals(t, []string{"FSSETXATTR +0x8"}, f.calls)

	// A flag the file system doesn't support is a warning, not an error.
	f = &fakeInodeFlags{refuse: badBit}
	err := restoreFlagsWith("file", data.NodeTypeFile, nodump|badBit, f.ops(false))
	var unsupported *UnsupportedError
	rtest.Assert(t, errors.As(err, &unsupported), "no UnsupportedError: %v", err)
	rtest.Equals(t, uint64(badBit), unsupported.Bits)
	rtest.Equals(t, uint32(nodump), f.flags)
}

// A file without a project ID (0, as every file has without project
// quotas) gets no attribute, and nothing fails where there are none.
func TestNodeFillProjectID(t *testing.T) {
	dir := rtest.TempDir(t)
	if id := projectIDOf(t, dir); id != 0 {
		t.Skipf("the temporary directory has project ID %d, which files in it may inherit", id)
	}
	path := filepath.Join(dir, "file")
	rtest.OK(t, os.WriteFile(path, []byte("data"), 0o600))
	fi, err := os.Lstat(path)
	rtest.OK(t, err)
	node, err := nodeFromFileInfo(path, ExtendedStat(fi), false, t.Logf)
	rtest.OK(t, err)
	_, ok := node.GenericAttributes[data.TypeLinuxProjectID]
	rtest.Assert(t, !ok, "project ID 0 recorded: %s", node.GenericAttributes[data.TypeLinuxProjectID])

	// Restoring a node without one leaves the file alone.
	rtest.OK(t, nodeRestoreOSAttributes(node, path))
}

// projectIDOf returns the project ID of the directory at path, 0 where there
// are none.
func projectIDOf(t *testing.T, path string) uint32 {
	fd, err := openForFlags(path, data.NodeTypeDir, nil)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	var fsx fsxattr
	if err := ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx); err != nil {
		return 0
	}
	return fsx.projid
}

// Restoring a project ID onto a file system without them is a warning, and a
// malformed or explicit 0 one is none or nothing to do.
func TestNodeRestoreProjectIDWarnings(t *testing.T) {
	dir := rtest.TempDir(t)
	path := filepath.Join(dir, "file")
	rtest.OK(t, os.WriteFile(path, []byte("data"), 0o600))
	node := &data.Node{Name: "file", Type: data.NodeTypeFile}

	node.GenericAttributes = map[data.GenericAttributeType]json.RawMessage{data.TypeLinuxProjectID: json.RawMessage(`0`)}
	rtest.OK(t, nodeRestoreOSAttributes(node, path))

	node.GenericAttributes[data.TypeLinuxProjectID] = json.RawMessage(`-1`)
	err := nodeRestoreOSAttributes(node, path)
	var malformed *data.MalformedAttributeError
	rtest.Assert(t, errors.As(err, &malformed), "no MalformedAttributeError: %v", err)

	node.GenericAttributes[data.TypeLinuxProjectID] = json.RawMessage(`7`)
	err = nodeRestoreOSAttributes(node, path)
	switch {
	case err == nil:
		rtest.Equals(t, uint32(7), projectIDOfFile(t, path))
	case IsWarning(err):
		t.Logf("no project IDs here: %v", err)
	case errors.Is(err, os.ErrPermission):
		t.Logf("project ID not restored (not root): %v", err)
	default:
		t.Fatalf("project ID not restored: %v", err)
	}
}

func projectIDOfFile(t *testing.T, path string) uint32 {
	fd, err := openForFlags(path, data.NodeTypeFile, nil)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	var fsx fsxattr
	rtest.OK(t, ioctlFsxattr(fd, fsIOCFSGetXattr, &fsx))
	return fsx.projid
}

// With root, on a loop btrfs: setting flags leaves a file's
// btrfs.compression property as it was restored (zstd), including with the
// immutable flag, which goes on through FS_IOC_FSSETXATTR.
func TestRestoreFileFlagsBtrfsCompression(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to mount a file system")
	}
	if _, err := exec.LookPath("mkfs.btrfs"); err != nil {
		t.Skip("no mkfs.btrfs")
	}
	tmp := rtest.TempDir(t)
	img := filepath.Join(tmp, "btrfs.img")
	mnt := filepath.Join(tmp, "mnt")
	rtest.OK(t, os.Mkdir(mnt, 0o755))
	rtest.OK(t, os.WriteFile(img, nil, 0o600))
	rtest.OK(t, os.Truncate(img, 128<<20))
	if out, err := exec.Command("mkfs.btrfs", "-q", img).CombinedOutput(); err != nil {
		t.Skipf("mkfs.btrfs: %v: %s", err, out)
	}
	if out, err := exec.Command("mount", "-o", "loop", img, mnt).CombinedOutput(); err != nil {
		t.Skipf("mount: %v: %s", err, out)
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()

	const (
		compr     = 0x00000004 // FS_COMPR_FL
		immutable = 0x00000010 // FS_IMMUTABLE_FL
		nodump    = 0x00000040 // FS_NODUMP_FL
	)
	for _, flags := range []uint32{compr | nodump, compr | nodump | immutable} {
		path := filepath.Join(mnt, fmt.Sprintf("file-%#x", flags))
		rtest.OK(t, os.WriteFile(path, nil, 0o600))
		rtest.OK(t, unix.Lsetxattr(path, "btrfs.compression", []byte("zstd"), 0))
		rtest.OK(t, os.WriteFile(path, []byte("data"), 0o600))

		rtest.OK(t, restoreFileFlags(path, data.NodeTypeFile, flags, []byte("zstd")))

		buf := make([]byte, 64)
		n, err := unix.Lgetxattr(path, "btrfs.compression", buf)
		rtest.OK(t, err)
		rtest.Equals(t, "zstd", string(buf[:n]))
		fd, err := openForFlags(path, data.NodeTypeFile, nil)
		rtest.OK(t, err)
		got, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
		_ = unix.Close(fd)
		rtest.OK(t, err)
		rtest.Equals(t, flags, got&linuxInodeFlags)
		if flags&immutable != 0 {
			_ = exec.Command("chattr", "-i", path).Run()
		}
	}
}

// bcachefs_effective.project is a number in decimal; anything else isn't
// taken, and FS_IOC_FSGETXATTR's ID stays.
func TestParseBcachefsProject(t *testing.T) {
	for value, want := range map[string]uint32{"42": 42, "42\n": 42, "42\x00": 42, " 7 ": 7, "0": 0, "4294967295": 4294967295} {
		id, ok := parseBcachefsProject([]byte(value))
		rtest.Assert(t, ok, "%q not read", value)
		rtest.Equals(t, want, id)
	}
	for _, value := range []string{"", "x", "-1", "4294967296", "0x2a"} {
		_, ok := parseBcachefsProject([]byte(value))
		rtest.Assert(t, !ok, "%q read", value)
	}
	// Off bcachefs, FS_IOC_FSGETXATTR's ID, without a look at the file.
	rtest.Equals(t, uint32(9), fileProjectID(-1, 9, false))
	// No such attribute: FS_IOC_FSGETXATTR's ID.
	rtest.Equals(t, uint32(9), fileProjectID(-1, 9, true))
}

// With root, on a loop bcachefs: a project ID set with chattr -p, which
// FS_IOC_FSGETXATTR reports as 0 until a remount (Linux 6.12), is backed up
// from bcachefs_effective.project, and a restore sets it.
func TestNodeProjectIDBcachefs(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to mount a file system")
	}
	for _, cmd := range []string{"bcachefs", "chattr"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("no %v", cmd)
		}
	}
	tmp := rtest.TempDir(t)
	img := filepath.Join(tmp, "bcachefs.img")
	mnt := filepath.Join(tmp, "mnt")
	rtest.OK(t, os.Mkdir(mnt, 0o755))
	rtest.OK(t, os.WriteFile(img, nil, 0o600))
	rtest.OK(t, os.Truncate(img, 256<<20))
	if out, err := exec.Command("bcachefs", "format", "-q", img).CombinedOutput(); err != nil {
		t.Skipf("bcachefs format: %v: %s", err, out)
	}
	if out, err := exec.Command("mount", "-t", "bcachefs", "-o", "loop", img, mnt).CombinedOutput(); err != nil {
		t.Skipf("mount: %v: %s", err, out)
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()

	path := filepath.Join(mnt, "file")
	rtest.OK(t, os.WriteFile(path, []byte("data"), 0o600))
	if out, err := exec.Command("chattr", "-p", "42", path).CombinedOutput(); err != nil {
		t.Fatalf("chattr -p: %v: %s", err, out)
	}
	fi, err := os.Lstat(path)
	rtest.OK(t, err)
	node, err := nodeFromFileInfo(path, ExtendedStat(fi), false, t.Logf)
	rtest.OK(t, err)
	id, ok, err := node.ProjectID()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "no project ID: %v", node.GenericAttributes)
	rtest.Equals(t, uint32(42), id)
	for _, attr := range node.ExtendedAttributes {
		rtest.Assert(t, !strings.HasPrefix(attr.Name, bcachefsEffective), "%v kept", attr.Name)
	}

	restored := filepath.Join(mnt, "restored")
	rtest.OK(t, os.WriteFile(restored, []byte("data"), 0o600))
	rtest.OK(t, nodeRestoreOSAttributes(node, restored))
	buf := make([]byte, 32)
	n, err := unix.Lgetxattr(restored, bcachefsProject, buf)
	rtest.OK(t, err)
	got, ok := parseBcachefsProject(buf[:n])
	rtest.Assert(t, ok, "%v: %q", bcachefsProject, buf[:n])
	rtest.Equals(t, uint32(42), got)
}
