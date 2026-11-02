//go:build linux

package fs

import (
	"os"
	"path/filepath"
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// fakeRestrictingFlags is a file's inode flags for clearRestrictingWith.
// zfs makes its fsx_xflags those of Ubuntu 24.04's OpenZFS, as it behaves:
// FS_IOC_GETFLAGS's flags, so that FS_XFLAG_APPEND clears FS_IMMUTABLE_FL; otherwise they are the kernel's
// FS_XFLAG_* bits.  noXattr refuses FS_IOC_FSSETXATTR (ENOTTY).
type fakeRestrictingFlags struct {
	flags        uint32
	zfs, noXattr bool
	sets         int
}

func (f *fakeRestrictingFlags) ops() restrictingFlagOps {
	return restrictingFlagOps{
		get: func() (uint32, error) { return f.flags, nil },
		set: func(flags uint32) error {
			f.sets++
			f.flags = flags
			return nil
		},
		clearXflags: func(xflags uint32) error {
			if f.noXattr {
				return unix.ENOTTY
			}
			if f.zfs {
				f.flags &^= xflags
				return nil
			}
			if xflags&fsXflagImmutable != 0 {
				f.flags &^= fsImmutableFl
			}
			if xflags&fsXflagAppend != 0 {
				f.flags &^= fsAppendFl
			}
			return nil
		},
	}
}

func TestClearRestrictingWith(t *testing.T) {
	const extents = 0x00080000 // FS_EXTENT_FL, the file system's own
	for _, tc := range []struct {
		name         string
		file         fakeRestrictingFlags
		wantSetflags int
	}{
		{"ext4 i+a", fakeRestrictingFlags{flags: extents | fsImmutableAppend}, 0},
		{"ext4 none", fakeRestrictingFlags{flags: extents}, 0},
		{"no FSSETXATTR", fakeRestrictingFlags{flags: extents | fsAppendFl, noXattr: true}, 1},
		// OpenZFS 2.2: FS_IOC_FSSETXATTR clears immutable, not append-only.
		{"zfs a", fakeRestrictingFlags{flags: fsAppendFl, zfs: true}, 1},
		{"zfs i+a", fakeRestrictingFlags{flags: fsImmutableAppend, zfs: true}, 1},
		{"zfs i", fakeRestrictingFlags{flags: fsImmutableFl, zfs: true}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.file
			rtest.OK(t, clearRestrictingWith("f", f.ops()))
			rtest.Equals(t, uint32(0), f.flags&fsImmutableAppend)
			rtest.Equals(t, tc.file.flags&^fsImmutableAppend, f.flags)
			rtest.Equals(t, tc.wantSetflags, f.sets)
		})
	}
}

// BackOtter: on a real file, i and a both go and the others stay, on the
// test's file system (ZFS too: TMPDIR on a dataset).  Setting i or a takes
// CAP_LINUX_IMMUTABLE (root); the test is skipped without it.
func TestClearRestrictingFlagsFile(t *testing.T) {
	const nodump = 0x00000040 // FS_NODUMP_FL
	path := filepath.Join(t.TempDir(), "f")
	rtest.OK(t, os.WriteFile(path, []byte("x"), 0o600))
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	rtest.OK(t, err)
	defer func() { _ = unix.Close(fd) }()
	current, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		t.Skipf("no inode flags: %v", err)
	}
	for _, flags := range []uint32{fsAppendFl, fsImmutableFl, fsImmutableAppend} {
		if err := unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(current|flags|nodump)); err != nil {
			t.Skipf("can't set flags %#x: %v", flags, err)
		}
		rtest.OK(t, clearRestrictingFlags(path))
		after, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
		rtest.OK(t, err)
		rtest.Equals(t, uint32(0), after&fsImmutableAppend)
		rtest.Assert(t, after&nodump != 0, "nodump cleared: %#x", after)
		rtest.OK(t, os.WriteFile(path, []byte("rewritten"), 0o600))
	}
}
