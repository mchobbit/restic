//go:build linux

package fs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
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
	// Casefolding is kept, and the file system's own flags stay.
	rtest.Equals(t, uint32(extents|casefold|immutable), mergeInodeFlags(extents|casefold, casefold|immutable))
	rtest.Equals(t, uint32(extents), mergeInodeFlags(extents|nocow, extents))
	// Flags that are right already need no FS_IOC_SETFLAGS.
	rtest.Equals(t, uint32(extents|nocow), mergeInodeFlags(extents|nocow, nocow))
}

// A file without a project ID (0, as every file has without project
// quotas) gets no attribute, and nothing fails where there are none.
func TestNodeFillProjectID(t *testing.T) {
	path := filepath.Join(rtest.TempDir(t), "file")
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
