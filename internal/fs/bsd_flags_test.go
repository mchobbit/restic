package fs

import (
	"fmt"
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

func TestKeptFreeBSDFileFlags(t *testing.T) {
	const (
		ufHidden   = 0x8000
		ufReadonly = 0x1000
		ufSystem   = 0x0080
	)
	for _, test := range []struct {
		flags uint32
		zfs   bool
		kept  bool
	}{
		// ZFS: archive alone is every new file's; none is archive cleared.
		{freebsdUFArchive, true, false},
		{0, true, true},
		{ufHidden, true, true},
		{ufHidden | freebsdUFArchive, true, true},
		{ufReadonly | ufSystem, true, true},
		// Elsewhere: neither archive alone nor none.
		{freebsdUFArchive, false, false},
		{0, false, false},
		{ufHidden, false, true},
		{ufHidden | freebsdUFArchive, false, true},
	} {
		rtest.Equals(t, test.kept, keptFreeBSDFileFlags(test.flags, test.zfs),
			fmt.Sprintf("flags %#x, zfs %v", test.flags, test.zfs))
	}
}
