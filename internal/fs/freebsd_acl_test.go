package fs

import (
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestNFS4TrivialACL(t *testing.T) {
	for _, mode := range []uint32{0o644, 0o755, 0o600, 0o000, 0o777, 0o604, 0o070, 0o007, 0o460} {
		acl := nfs4TrivialACL(mode)
		rtest.Assert(t, nfs4IsTrivial(acl), "trivial ACL for %o not trivial: %v", mode, acl)
		rtest.Equals(t, mode, nfs4ModeFromACL(acl))
	}

	// What getfacl(1) shows ZFS giving a new 0644 file:
	//   owner@:rw-p--aARWcCos:-------:allow
	//   group@:r-----a-R-c--s:-------:allow
	//   everyone@:r-----a-R-c--s:-------:allow
	read := uint32(aclReadData | aclReadAttributes | aclReadNamedAttrs | aclReadACL | aclSynchronize)
	zfs := []aclEntry{
		{Tag: aclUserObj, ID: aclUndefinedID, Type: aclEntryTypeAllow,
			Perm: read | aclWriteData | aclAppendData | aclWriteAttributes | aclWriteNamedAttrs | aclWriteACL | aclWriteOwner},
		{Tag: aclGroupObj, ID: aclUndefinedID, Type: aclEntryTypeAllow, Perm: read},
		{Tag: aclEveryone, ID: aclUndefinedID, Type: aclEntryTypeAllow, Perm: read},
	}
	rtest.Assert(t, nfs4IsTrivial(zfs), "ZFS's trivial ACL for 0644 not trivial")
	rtest.Equals(t, uint32(0o644), nfs4ModeFromACL(zfs))

	named := append([]aclEntry{{Tag: aclUser, ID: 65534, Type: aclEntryTypeAllow, Perm: aclReadData}}, zfs...)
	rtest.Assert(t, !nfs4IsTrivial(named), "ACL with a named user is trivial")

	inherit := append([]aclEntry(nil), zfs...)
	inherit[2].Flags = 0x0001 // file_inherit
	rtest.Assert(t, !nfs4IsTrivial(inherit), "ACL with an inheritable entry is trivial")

	deny := append([]aclEntry{{Tag: aclGroupObj, ID: aclUndefinedID, Type: aclEntryTypeDeny, Perm: aclWriteACL}}, zfs...)
	rtest.Assert(t, !nfs4IsTrivial(deny), "ACL with an extra deny entry is trivial")
}

func TestPOSIXOwningGroupPerms(t *testing.T) {
	// setfacl -m u:nobody:rwx,g:wheel:r,m::rwx on a 0644 file: the mode's
	// group bits become the mask, rwx, though the owning group has only r.
	acl := []aclEntry{
		{Tag: aclUserObj, ID: aclUndefinedID, Perm: 6},
		{Tag: aclUser, ID: 65534, Perm: 7},
		{Tag: aclGroupObj, ID: aclUndefinedID, Perm: 4},
		{Tag: aclGroup, ID: 0, Perm: 4},
		{Tag: aclMask, ID: aclUndefinedID, Perm: 7},
		{Tag: aclOther, ID: aclUndefinedID, Perm: 4},
	}
	perms, ok := posixOwningGroupPerms(acl)
	rtest.Assert(t, ok, "no group_obj entry found")
	rtest.Equals(t, uint32(4), perms)

	// A mask narrower than group_obj limits it.
	acl[2].Perm, acl[4].Perm = 7, 5
	perms, _ = posixOwningGroupPerms(acl)
	rtest.Equals(t, uint32(5), perms)
}
