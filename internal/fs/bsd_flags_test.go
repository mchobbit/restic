package fs

import (
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestBSDFileFlagMasks(t *testing.T) {
	// NetBSD: SF_SNAPSHOT, SF_LOG and SF_SNAPINVAL are dropped; arch, uchg
	// and nodump kept.
	for _, bit := range []uint32{0x200000, 0x400000, 0x800000} {
		rtest.Equals(t, uint32(0), bit&netbsdFileFlags)
	}
	rtest.Equals(t, uint32(0x10000|0x2|0x1), (0x10000|0x2|0x1)&netbsdFileFlags)
	rtest.Equals(t, uint32(0xff1fffff), uint32(netbsdFileFlags))

	// DragonFly keeps every bit.
	rtest.Equals(t, uint32(0xffffffff), uint32(dragonflyFileFlags))
}
