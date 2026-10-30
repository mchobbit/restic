//go:build freebsd || netbsd

package fs

import (
	"testing"
	"unsafe"

	rtest "github.com/restic/restic/internal/test"
)

// The kernel copies exactly sizeof(struct acl) bytes in and out.
func TestRawACLSize(t *testing.T) {
	rtest.Equals(t, uintptr(8+4*4+aclMaxEntries*16), unsafe.Sizeof(rawACL{}))
}
