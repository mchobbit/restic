//go:build linux

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// ZFS DOS flags kept elsewhere, or not at all.
const (
	zfsImmutable  = 0x0000001000000000 // ZFS_IMMUTABLE, i
	zfsAppendonly = 0x0000004000000000 // ZFS_APPENDONLY, a
	zfsNodump     = 0x0000008000000000 // ZFS_NODUMP, d
	zfsAvModified = 0x0000040000000000 // ZFS_AV_MODIFIED, not shown
	zfsReparse    = 0x0000080000000000 // ZFS_REPARSE, not settable
)

func TestZFSDosFlagsIoctls(t *testing.T) {
	switch runtime.GOARCH {
	case "amd64", "arm64", "386", "arm", "riscv64", "loong64", "s390x":
		rtest.Equals(t, uint(0x80088301), zfsIOCGetDosFlags)
		rtest.Equals(t, uint(0x40088302), zfsIOCSetDosFlags)
	case "mips", "mipsle", "mips64", "mips64le", "ppc64", "ppc64le":
		rtest.Equals(t, uint(0x40088301), zfsIOCGetDosFlags)
		rtest.Equals(t, uint(0x80088302), zfsIOCSetDosFlags)
	}
	rtest.Equals(t, uint64(0x302f00000000), uint64(zfsDosFlags))
}

func TestKeptZFSDosFlags(t *testing.T) {
	// Archive alone, as every new or written file has: no attribute.
	_, ok := keptZFSDosFlags(zfsArchive)
	rtest.Assert(t, !ok, "archive alone kept")
	_, ok = keptZFSDosFlags(zfsArchive | zfsImmutable | zfsAppendonly | zfsNodump | zfsReparse | zfsAvModified)
	rtest.Assert(t, !ok, "archive and flags kept elsewhere kept")
	// None at all: 0, so that the restored file loses the archive flag
	// its write gives it.
	kept, ok := keptZFSDosFlags(0)
	rtest.Assert(t, ok, "archive cleared not kept")
	rtest.Equals(t, uint64(0), kept)
	// Immutable, append-only and nodump (inode flags), reparse and the
	// flags the ioctl doesn't set are never stored.
	kept, ok = keptZFSDosFlags(zfsHidden | zfsImmutable | zfsAppendonly | zfsNodump | zfsReparse | zfsAvModified)
	rtest.Assert(t, ok, "hidden not kept")
	rtest.Equals(t, uint64(zfsHidden), kept)
	kept, ok = keptZFSDosFlags(^uint64(0))
	rtest.Assert(t, ok, "every flag not kept")
	rtest.Equals(t, uint64(0x302f00000000), kept)
}

func TestMergeZFSDosFlags(t *testing.T) {
	// Immutable, append-only, nodump and reparse stay as the file has
	// them; the kept flags become those restored, archive cleared too.
	rtest.Equals(t, uint64(zfsImmutable|zfsNodump|zfsHidden|zfsSystem),
		mergeZFSDosFlags(zfsImmutable|zfsNodump|zfsArchive, zfsHidden|zfsSystem|zfsAppendonly))
	rtest.Equals(t, uint64(zfsReparse|zfsAppendonly), mergeZFSDosFlags(zfsReparse|zfsAppendonly|zfsArchive, 0))
	rtest.Equals(t, uint64(zfsArchive), mergeZFSDosFlags(zfsArchive, zfsArchive))
}

// fakeDosFlags is a file's ZFS DOS flags for restoreZFSDosFlagsWith,
// recording the calls made.
type fakeDosFlags struct {
	flags     uint64
	calls     []string
	refuse    uint64 // flags SETDOSFLAGS refuses
	refuseAll bool   // SETDOSFLAGS refuses any mask
	err       error  // the error refusing them, EOPNOTSUPP if nil
}

func (f *fakeDosFlags) get() (uint64, error) {
	return f.flags, nil
}

func (f *fakeDosFlags) set(flags uint64) error {
	f.calls = append(f.calls, fmt.Sprintf("SETDOSFLAGS %#x", flags))
	if f.refuseAll || flags&f.refuse != 0 {
		if f.err != nil {
			return f.err
		}
		return unix.EOPNOTSUPP
	}
	f.flags = flags
	return nil
}

func TestRestoreZFSDosFlagsWith(t *testing.T) {
	// Unchanged: no SETDOSFLAGS at all.
	f := &fakeDosFlags{flags: zfsArchive | zfsNodump}
	rtest.OK(t, restoreZFSDosFlagsWith("file", zfsArchive, f.get, f.set))
	rtest.Equals(t, []string(nil), f.calls)

	// Changed: one SETDOSFLAGS of the whole mask, which keeps immutable
	// and nodump as the file has them.
	f = &fakeDosFlags{flags: zfsArchive | zfsImmutable | zfsNodump}
	rtest.OK(t, restoreZFSDosFlagsWith("file", zfsHidden|zfsSystem, f.get, f.set))
	rtest.Equals(t, []string{"SETDOSFLAGS 0x9600000000"}, f.calls)
	rtest.Equals(t, uint64(zfsImmutable|zfsNodump|zfsHidden|zfsSystem), f.flags)

	// Archive cleared: 0 is a value.
	f = &fakeDosFlags{flags: zfsArchive}
	rtest.OK(t, restoreZFSDosFlagsWith("file", 0, f.get, f.set))
	rtest.Equals(t, uint64(0), f.flags)

	// A flag the file system doesn't support: the others are set, one at a
	// time, and the refused one is a warning naming it.
	f = &fakeDosFlags{flags: zfsArchive, refuse: zfsOffline}
	err := restoreZFSDosFlagsWith("file", zfsHidden|zfsOffline|zfsSparse, f.get, f.set)
	var unsupported *UnsupportedError
	rtest.Assert(t, errors.As(err, &unsupported), "no UnsupportedError: %v", err)
	rtest.Equals(t, uint64(zfsOffline), unsupported.Bits)
	rtest.Equals(t, data.TypeLinuxZFSDosFlags, unsupported.Attr)
	rtest.Assert(t, IsWarning(err), "unsupported ZFS DOS flags are an error: %v", err)
	rtest.Equals(t, uint64(zfsHidden|zfsSparse), f.flags)

	// Refused for lack of permission (not the owner): an error naming
	// the flags, not a warning.
	f = &fakeDosFlags{flags: zfsArchive, refuse: zfsHidden, err: unix.EACCES}
	err = restoreZFSDosFlagsWith("file", zfsHidden|zfsSystem, f.get, f.set)
	rtest.Assert(t, errors.Is(err, unix.EACCES), "error %v doesn't wrap EACCES", err)
	rtest.Assert(t, !IsWarning(err), "a refused flag is a warning: %v", err)
	rtest.Equals(t, uint64(zfsSystem), f.flags)

	// Clearing them all refused: still an error, though there is no bit
	// to retry.
	f = &fakeDosFlags{flags: zfsArchive, refuseAll: true, err: unix.EACCES}
	err = restoreZFSDosFlagsWith("file", 0, f.get, f.set)
	rtest.Assert(t, errors.Is(err, unix.EACCES), "error %v doesn't wrap EACCES", err)
	rtest.Equals(t, uint64(zfsArchive), f.flags)
}

// Restoring ZFS DOS flags onto another file system is a warning.
func TestRestoreZFSDosFlagsNotZFS(t *testing.T) {
	dir := rtest.TempDir(t)
	if onZFS(t, dir) {
		t.Skip("the temporary directory is on ZFS")
	}
	path := filepath.Join(dir, "file")
	rtest.OK(t, os.WriteFile(path, []byte("data"), 0o600))
	err := restoreZFSDosFlags(path, data.NodeTypeFile, zfsHidden)
	var unsupported *UnsupportedError
	rtest.Assert(t, errors.As(err, &unsupported) && errors.Is(err, errNotZFS), "no UnsupportedError for not ZFS: %v", err)
	rtest.Equals(t, data.TypeLinuxZFSDosFlags, unsupported.Attr)
}

func onZFS(t *testing.T, path string) bool {
	var st unix.Statfs_t
	rtest.OK(t, unix.Statfs(path, &st))
	return uint32(st.Type) == zfsSuperMagic
}

func dosFlagsOf(t *testing.T, path string, typ data.NodeType) uint64 {
	t.Helper()
	fd, err := openForFlags(path, typ, nil)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	var dos uint64
	rtest.OK(t, ioctlDosFlags(fd, zfsIOCGetDosFlags, &dos))
	return dos
}

func setDosFlags(t *testing.T, path string, typ data.NodeType, dos uint64) {
	t.Helper()
	fd, err := openForFlags(path, typ, nil)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	rtest.OK(t, ioctlDosFlags(fd, zfsIOCSetDosFlags, &dos))
}

// On ZFS (TMPDIR on a ZFS dataset): files and a directory with DOS flags
// get the attribute, and a restore onto new ones sets exactly those, while
// a plain file gets none.  The flags tried are those an owner may set.
func TestZFSDosFlagsRoundTrip(t *testing.T) {
	dir := rtest.TempDir(t)
	if !onZFS(t, dir) {
		t.Skip("the temporary directory isn't on ZFS")
	}
	type item struct {
		name string
		typ  data.NodeType
		dos  uint64
	}
	items := []item{
		{"plain", data.NodeTypeFile, zfsArchive},
		{"cleared", data.NodeTypeFile, 0},
		{"hidden-system", data.NodeTypeFile, zfsHidden | zfsSystem | zfsArchive},
		{"readonly", data.NodeTypeFile, zfsReadonly},
		{"nounlink", data.NodeTypeFile, zfsNounlink},
		{"offline-sparse", data.NodeTypeFile, zfsOffline | zfsSparse},
		{"dir", data.NodeTypeDir, zfsHidden},
	}
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	rtest.OK(t, os.Mkdir(src, 0o755))
	rtest.OK(t, os.Mkdir(dst, 0o755))
	clear := func(path string, typ data.NodeType) {
		// Readonly and nounlink would keep the test from removing them.
		setDosFlags(t, path, typ, zfsArchive)
	}
	for _, it := range items {
		for _, base := range []string{src, dst} {
			path := filepath.Join(base, it.name)
			if it.typ == data.NodeTypeDir {
				rtest.OK(t, os.Mkdir(path, 0o755))
			} else {
				rtest.OK(t, os.WriteFile(path, []byte("data"), 0o644))
			}
			defer clear(path, it.typ)
		}
		path := filepath.Join(src, it.name)
		setDosFlags(t, path, it.typ, it.dos)
		rtest.Equals(t, it.dos, dosFlagsOf(t, path, it.typ))

		fi, err := os.Lstat(path)
		rtest.OK(t, err)
		node, err := nodeFromFileInfo(path, ExtendedStat(fi), false, t.Logf)
		rtest.OK(t, err)
		dos, ok, err := NodeZFSDosFlags(node)
		rtest.OK(t, err)
		rtest.Equals(t, it.dos != zfsArchive, ok)
		if !ok {
			continue
		}
		rtest.Equals(t, it.dos, dos)

		target := filepath.Join(dst, it.name)
		rtest.OK(t, RestoreZFSDosFlags(target, it.typ, dos))
		rtest.Equals(t, it.dos, dosFlagsOf(t, target, it.typ))
	}
}
