//go:build freebsd

package fs

import (
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// freebsdACLs spells out the numbers x/sys/unix has for FreeBSD.
func TestFreeBSDACLSyscalls(t *testing.T) {
	rtest.Equals(t, uintptr(unix.SYS___ACL_GET_LINK), osACLs.sysACLGetLink)
	rtest.Equals(t, uintptr(unix.SYS___ACL_SET_LINK), osACLs.sysACLSetLink)
	rtest.Equals(t, uintptr(unix.SYS_LPATHCONF), osACLs.sysLpathconf)
}
