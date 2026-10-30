package fs

import (
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestACEIsTrivial(t *testing.T) {
	// What ls -V shows ZFS on illumos giving a new 0644 file.
	const read, write = 0x1, 0x2 | 0x4 // read_data; write_data, append_data
	const attrs = aceReadAttributes | 0x8 | aceReadACL | 0x100000
	trivial := []aceEntry{
		{Who: 0xffffffff, Flags: aceOwner, Type: aceAccessAllowed,
			Mask: read | write | attrs | aceWriteAttributes | aceWriteNamedAttrs | aceWriteACL | aceWriteOwner},
		{Who: 0xffffffff, Flags: aceGroup | aceIdentifierGroup, Type: aceAccessAllowed, Mask: read | attrs},
		{Who: 0xffffffff, Flags: aceEveryone, Type: aceAccessAllowed, Mask: read | attrs},
	}
	rtest.Assert(t, aceIsTrivial(trivial), "ZFS's trivial ACL for 0644 not trivial")

	named := append([]aceEntry{{Who: 60001, Mask: read, Type: aceAccessAllowed}}, trivial...)
	rtest.Assert(t, !aceIsTrivial(named), "ACL with a named user is trivial")

	deny := append([]aceEntry{{Who: 10, Flags: aceIdentifierGroup, Mask: read, Type: aceAccessDenied}}, trivial...)
	rtest.Assert(t, !aceIsTrivial(deny), "ACL with a named group's deny entry is trivial")

	inherit := append([]aceEntry(nil), trivial...)
	inherit[2].Flags |= aceFileInherit
	rtest.Assert(t, !aceIsTrivial(inherit), "ACL with an inheritable entry is trivial")

	writeACL := append([]aceEntry(nil), trivial...)
	writeACL[2].Mask |= aceWriteACL
	rtest.Assert(t, !aceIsTrivial(writeACL), "everyone@ allowed write_acl is trivial")
}
