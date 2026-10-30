package fs

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/restic/restic/internal/data"
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

func TestBSDACLSystems(t *testing.T) {
	// pathconf(2) names, from each system's <sys/unistd.h>.
	rtest.Equals(t, 59, freebsdACLs.pcACLExtended)
	rtest.Equals(t, 64, freebsdACLs.pcACLNFS4)
	rtest.Equals(t, 14, netbsdACLs.pcACLExtended)
	rtest.Equals(t, 17, netbsdACLs.pcACLNFS4)

	// NetBSD's system calls, from its <sys/syscall.h>.
	rtest.Equals(t, uintptr(487), netbsdACLs.sysACLGetLink)
	rtest.Equals(t, uintptr(488), netbsdACLs.sysACLSetLink)
	rtest.Equals(t, uintptr(499), netbsdACLs.sysLpathconf)

	rtest.Equals(t, data.GenericAttributeType("netbsd.acl_access"), netbsdACLs.aclAccess)
	rtest.Equals(t, data.GenericAttributeType("netbsd.acl_default"), netbsdACLs.aclDefault)
	rtest.Equals(t, data.GenericAttributeType("netbsd.acl_nfs4"), netbsdACLs.aclNFS4)
	rtest.Equals(t, data.GenericAttributeType("netbsd.system_extattrs"), netbsdACLs.systemExtattrs)
	rtest.Equals(t, data.GenericAttributeType("freebsd.acl_access"), freebsdACLs.aclAccess)
	rtest.Equals(t, data.GenericAttributeType("freebsd.acl_default"), freebsdACLs.aclDefault)
	rtest.Equals(t, data.GenericAttributeType("freebsd.acl_nfs4"), freebsdACLs.aclNFS4)
	rtest.Equals(t, data.GenericAttributeType("freebsd.system_extattrs"), freebsdACLs.systemExtattrs)
}

// fillACLs keeps the same ACLs, in the same JSON, on FreeBSD and NetBSD;
// only the attributes' prefixes differ.
func TestFillACLsBothSystems(t *testing.T) {
	access := []aclEntry{
		{Tag: aclUserObj, ID: aclUndefinedID, Perm: 6},
		{Tag: aclUser, ID: 1001, Perm: 7},
		{Tag: aclGroupObj, ID: aclUndefinedID, Perm: 4},
		{Tag: aclMask, ID: aclUndefinedID, Perm: 7},
		{Tag: aclOther, ID: aclUndefinedID, Perm: 4},
	}
	trivialAccess := []aclEntry{access[0], access[2], access[4]}
	defaultACL := []aclEntry{
		{Tag: aclUserObj, ID: aclUndefinedID, Perm: 7},
		{Tag: aclGroupObj, ID: aclUndefinedID, Perm: 5},
		{Tag: aclOther, ID: aclUndefinedID, Perm: 5},
	}
	nfs4 := append([]aclEntry{{Tag: aclUser, ID: 1001, Type: aclEntryTypeDeny, Perm: aclWriteData}},
		nfs4TrivialACL(0o644)...)

	fill := func(sys *bsdACLSystem, typ data.NodeType, isNFS4 bool, acls map[int][]aclEntry) map[string]string {
		node := &data.Node{Type: typ}
		err := sys.fillACLs(node, isNFS4, func(aclType int) ([]aclEntry, error) {
			return acls[aclType], nil
		})
		rtest.OK(t, err)
		got := make(map[string]string)
		for name, value := range node.GenericAttributes {
			// "freebsd.acl_access" and "netbsd.acl_access" are both "acl_access".
			_, short, _ := strings.Cut(string(name), ".")
			got[short] = string(value)
		}
		return got
	}

	for _, test := range []struct {
		typ  data.NodeType
		nfs4 bool
		acls map[int][]aclEntry
		want []string
	}{
		{data.NodeTypeFile, false, map[int][]aclEntry{aclTypeAccess: access}, []string{"acl_access"}},
		{data.NodeTypeFile, false, map[int][]aclEntry{aclTypeAccess: trivialAccess}, nil},
		{data.NodeTypeDir, false, map[int][]aclEntry{aclTypeAccess: trivialAccess, aclTypeDefault: defaultACL}, []string{"acl_default"}},
		{data.NodeTypeDir, false, map[int][]aclEntry{aclTypeAccess: access, aclTypeDefault: defaultACL}, []string{"acl_access", "acl_default"}},
		{data.NodeTypeFile, true, map[int][]aclEntry{aclTypeNFS4: nfs4}, []string{"acl_nfs4"}},
		{data.NodeTypeFile, true, map[int][]aclEntry{aclTypeNFS4: nfs4TrivialACL(0o644)}, nil},
	} {
		freebsd := fill(&freebsdACLs, test.typ, test.nfs4, test.acls)
		netbsd := fill(&netbsdACLs, test.typ, test.nfs4, test.acls)
		rtest.Equals(t, freebsd, netbsd)
		rtest.Equals(t, test.want, slices.Sorted(maps.Keys(freebsd)))
	}

	// The JSON form, field by field.
	node := &data.Node{Type: data.NodeTypeFile}
	rtest.OK(t, netbsdACLs.fillACLs(node, true, func(int) ([]aclEntry, error) { return nfs4[:1], nil }))
	rtest.Equals(t, `[{"tag":2,"id":1001,"perm":16,"type":512}]`, string(node.GenericAttributes[data.TypeNetBSDACLNFS4]))

	// An error reading an ACL is returned as it is.
	rtest.Assert(t, netbsdACLs.fillACLs(&data.Node{}, false, func(int) ([]aclEntry, error) {
		return nil, errors.New("no")
	}) != nil, "no error")
}
