//go:build freebsd

package fs

import (
	"testing"
	"unsafe"

	rtest "github.com/restic/restic/internal/test"
)

func TestFreeBSDZfsxattrSize(t *testing.T) {
	// _IOR('X', 31, 28): the size is in the request.
	rtest.Equals(t, uintptr(28), unsafe.Sizeof(zfsxattr{}))
	rtest.Equals(t, uintptr(28), uintptr(zfsIOCFSGetXattr>>16&0x1fff))
	rtest.Equals(t, uintptr(28), uintptr(zfsIOCFSSetXattr>>16&0x1fff))
}
