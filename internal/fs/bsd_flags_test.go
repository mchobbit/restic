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

func TestRestrictingFlags(t *testing.T) {
	// chflags(1): uchg uappnd uunlnk urdonly schg sappnd sunlnk, as
	// rustic-bko's bsd_flags.rs has them.
	rtest.Equals(t, uint32(0x00161016), uint32(freebsdRestrictingFlags))
	rtest.Equals(t, uint32(0x00060006), uint32(netbsdRestrictingFlags))
	rtest.Equals(t, uint32(0x00060006), uint32(openbsdRestrictingFlags))
	rtest.Equals(t, uint32(0x00160016), uint32(dragonflyRestrictingFlags))
	// nodump, uarch, hidden and the snapshot bit stay.
	for _, keep := range []uint32{0x1, 0x800, 0x8000, 0x200000} {
		rtest.Equals(t, uint32(0), keep&freebsdRestrictingFlags)
	}
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
		// Elsewhere: any, archive alone too, but not none.
		{freebsdUFArchive, false, true},
		{0, false, false},
		{ufHidden, false, true},
		{ufHidden | freebsdUFArchive, false, true},
		{ufReadonly | ufSystem, false, true},
	} {
		rtest.Equals(t, test.kept, keptFreeBSDFileFlags(test.flags, test.zfs),
			fmt.Sprintf("flags %#x, zfs %v", test.flags, test.zfs))
	}
}
