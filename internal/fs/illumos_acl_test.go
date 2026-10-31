package fs

import (
	"encoding/json"
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

func TestACESID(t *testing.T) {
	// The ACL of a file with usersid: and groupsid: entries (ls -V), as
	// illumos.acl keeps it.
	entries := []aceEntry{
		{Who: 2147483650, Mask: 1, SID: "S-1-5-21-1111-2222-3333-1004"},
		{Who: 2147483651, Mask: 2, Flags: aceIdentifierGroup, Type: aceAccessDenied, SID: "S-1-5-21-1111-2222-3333-1005"},
		{Who: 0xffffffff, Mask: 0x1e019f, Flags: aceOwner},
		{Who: 60001, Mask: 1},
	}
	const want = `[{"who":2147483650,"mask":1,"sid":"S-1-5-21-1111-2222-3333-1004"},` +
		`{"who":2147483651,"mask":2,"flags":64,"type":1,"sid":"S-1-5-21-1111-2222-3333-1005"},` +
		`{"who":4294967295,"mask":1966495,"flags":4096},{"who":60001,"mask":1}]`
	buf, err := json.Marshal(entries)
	rtest.OK(t, err)
	rtest.Equals(t, want, string(buf))
	var back []aceEntry
	rtest.OK(t, json.Unmarshal(buf, &back))
	rtest.Equals(t, entries, back)

	// A reader without "sid" (restic-bko before it) gets the numbers.
	var old []struct {
		Who   uint32 `json:"who"`
		Mask  uint32 `json:"mask"`
		Flags uint16 `json:"flags,omitempty"`
		Type  uint16 `json:"type,omitempty"`
	}
	rtest.OK(t, json.Unmarshal(buf, &old))
	rtest.Equals(t, 4, len(old))
	rtest.Equals(t, uint32(2147483651), old[1].Who)
	rtest.Equals(t, uint16(aceIdentifierGroup), old[1].Flags)

	// Which entries get a SID: ephemeral ids, not owner@, group@ or
	// everyone@ (whose who is -1).
	for i, named := range []bool{true, true, false, false} {
		rtest.Equals(t, named, aceNamesSID(entries[i]))
	}
	rtest.Assert(t, !aceNamesSID(aceEntry{Who: 0xffffffff, Flags: aceGroup | aceIdentifierGroup}), "group@ names a SID")
	rtest.Assert(t, !aceNamesSID(aceEntry{Who: illumosMaxUID}), "MAXUID names a SID")
}

func TestSplitSID(t *testing.T) {
	prefix, rid, err := splitSID("S-1-5-21-1111-2222-3333-1001")
	rtest.OK(t, err)
	rtest.Equals(t, "S-1-5-21-1111-2222-3333", prefix)
	rtest.Equals(t, uint32(1001), rid)

	prefix, rid, err = splitSID("S-1-5-32-544")
	rtest.OK(t, err)
	rtest.Equals(t, "S-1-5-32", prefix)
	rtest.Equals(t, uint32(544), rid)

	for _, sid := range []string{"", "S", "1001", "-1001", "S-1-5-21-", "S-1-5-21-x", "S-1-5-21-4294967296", "X-1-5-1"} {
		_, _, err := splitSID(sid)
		rtest.Assert(t, err != nil, "malformed SID %q split", sid)
	}
}
