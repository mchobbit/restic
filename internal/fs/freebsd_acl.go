package fs

import (
	"slices"

	"github.com/restic/restic/internal/data"
)

// FreeBSD ACLs as <sys/acl.h> has them, and the part of FreeBSD's ACL code
// (sys/kern/subr_acl_nfs4.c) that tells a trivial NFSv4 ACL, one that says no
// more than the file's mode, from one worth keeping.  NetBSD has the same
// ACLs, struct acl and system calls, under other numbers.  Plain Go, so that
// it is tested on any OS; the system calls are in attrs_bsd.go.

const (
	aclMaxEntries  = 254        // ACL_MAX_ENTRIES
	aclTypeAccess  = 2          // ACL_TYPE_ACCESS
	aclTypeDefault = 3          // ACL_TYPE_DEFAULT
	aclTypeNFS4    = 4          // ACL_TYPE_NFS4
	aclUndefinedID = 0xffffffff // ACL_UNDEFINED_ID

	// ae_tag
	aclUserObj  = 0x01
	aclUser     = 0x02
	aclGroupObj = 0x04
	aclGroup    = 0x08
	aclMask     = 0x10
	aclOther    = 0x20
	aclEveryone = 0x40

	// ae_perm, POSIX.1e: the same bits as a mode's rwx
	aclPOSIXPerms = 0x7

	// ae_perm, NFSv4
	aclExecute         = 0x00000001
	aclReadData        = 0x00000008
	aclWriteData       = 0x00000010
	aclAppendData      = 0x00000020
	aclReadNamedAttrs  = 0x00000040
	aclWriteNamedAttrs = 0x00000080
	aclReadAttributes  = 0x00000200
	aclWriteAttributes = 0x00000400
	aclReadACL         = 0x00001000
	aclWriteACL        = 0x00002000
	aclWriteOwner      = 0x00004000
	aclSynchronize     = 0x00008000

	// ae_entry_type, NFSv4
	aclEntryTypeAllow = 0x0100
	aclEntryTypeDeny  = 0x0200

	// ae_flags, NFSv4
	aclEntryInheritOnly = 0x0008
)

// aclEntry is one entry of a FreeBSD ACL (struct acl_entry), as restic keeps
// it: ids are numeric, as the file's owner and group are.
type aclEntry struct {
	Tag   uint32 `json:"tag"`
	ID    uint32 `json:"id"`
	Perm  uint32 `json:"perm"`
	Type  uint16 `json:"type,omitempty"`
	Flags uint16 `json:"flags,omitempty"`
}

// bsdACLSystem is what differs between the systems with FreeBSD's ACLs and
// extended attributes: the numbers of the system calls x/sys/unix doesn't
// have for all of them, the pathconf(2) names, whether a symlink has ACLs of
// its own, and the generic attributes they are kept in.  The JSON is the
// same.
type bsdACLSystem struct {
	sysACLGetLink, sysACLSetLink, sysLpathconf uintptr // <sys/syscall.h>
	pcACLExtended, pcACLNFS4                   int     // <sys/unistd.h>
	symlinkACLs                                bool

	systemExtattrs, aclAccess, aclDefault, aclNFS4 data.GenericAttributeType
}

var freebsdACLs = bsdACLSystem{
	sysACLGetLink: 425, sysACLSetLink: 426, sysLpathconf: 513,
	pcACLExtended: 59, pcACLNFS4: 64,
	systemExtattrs: data.TypeFreeBSDSystemExtattrs,
	aclAccess:      data.TypeFreeBSDACLAccess,
	aclDefault:     data.TypeFreeBSDACLDefault,
	aclNFS4:        data.TypeFreeBSDACLNFS4,
}

var netbsdACLs = bsdACLSystem{
	sysACLGetLink: 487, sysACLSetLink: 488, sysLpathconf: 499,
	pcACLExtended: 14, pcACLNFS4: 17,
	symlinkACLs:    true, // setfacl -h works on FFSv2ea
	systemExtattrs: data.TypeNetBSDSystemExtattrs,
	aclAccess:      data.TypeNetBSDACLAccess,
	aclDefault:     data.TypeNetBSDACLDefault,
	aclNFS4:        data.TypeNetBSDACLNFS4,
}

// fillACLs keeps those of a file's ACLs that say more than its mode in
// node's generic attributes: its NFSv4 ACL if nfs4 says the file system has
// those, else its POSIX.1e access ACL and a directory's default ACL.  get
// reads the file's ACL of a type; its errors are returned as they are.
func (sys *bsdACLSystem) fillACLs(node *data.Node, nfs4 bool, get func(typ int) ([]aclEntry, error)) error {
	if nfs4 {
		acl, err := get(aclTypeNFS4)
		if err != nil || nfs4IsTrivial(acl) {
			return err
		}
		return node.SetGenericAttribute(sys.aclNFS4, acl)
	}
	acl, err := get(aclTypeAccess)
	if err != nil {
		return err
	}
	// Three entries are just the mode: user_obj, group_obj and other.
	if len(acl) > 3 {
		if err := node.SetGenericAttribute(sys.aclAccess, acl); err != nil {
			return err
		}
	}
	if node.Type != data.NodeTypeDir {
		return nil
	}
	acl, err = get(aclTypeDefault)
	if err != nil || len(acl) == 0 {
		return err
	}
	return node.SetGenericAttribute(sys.aclDefault, acl)
}

// nfs4ModeFromACL is acl_nfs4_sync_mode_from_acl(): the permission bits of a
// file's mode as FreeBSD derives them from its NFSv4 ACL.
func nfs4ModeFromACL(acl []aclEntry) uint32 {
	var mode, seen uint32
	for _, e := range acl {
		if e.Type != aclEntryTypeAllow && e.Type != aclEntryTypeDeny {
			continue
		}
		if e.Flags&aclEntryInheritOnly != 0 {
			continue
		}
		// The mode bits for read, write and execute this entry speaks for.
		var r, w, x uint32
		switch e.Tag {
		case aclUserObj:
			r, w, x = 0o400, 0o200, 0o100
		case aclGroupObj:
			r, w, x = 0o040, 0o020, 0o010
		case aclEveryone:
			r, w, x = 0o444, 0o222, 0o111
		default:
			continue
		}
		// The first entry to mention a bit decides it.
		decide := func(bits uint32) {
			bits &^= seen
			seen |= bits
			if e.Type == aclEntryTypeAllow {
				mode |= bits
			}
		}
		if e.Perm&aclReadData != 0 {
			decide(r)
		}
		if e.Perm&aclWriteData != 0 {
			decide(w)
		}
		if e.Perm&aclExecute != 0 {
			decide(x)
		}
	}
	return mode
}

// nfs4TrivialACL is acl_nfs4_trivial_from_mode(): the NFSv4 ACL FreeBSD gives
// a file with this mode (PSARC/2010/029 semantics, FreeBSD's since 9.0).
func nfs4TrivialACL(mode uint32) []aclEntry {
	group := uint32(aclReadACL | aclReadAttributes | aclReadNamedAttrs | aclSynchronize)
	everyone := group
	user := group | aclWriteACL | aclWriteOwner | aclWriteAttributes | aclWriteNamedAttrs
	rwx := func(perm *uint32, r, w, x uint32) {
		if mode&r != 0 {
			*perm |= aclReadData
		}
		if mode&w != 0 {
			*perm |= aclWriteData | aclAppendData
		}
		if mode&x != 0 {
			*perm |= aclExecute
		}
	}
	rwx(&user, 0o400, 0o200, 0o100)
	rwx(&group, 0o040, 0o020, 0o010)
	rwx(&everyone, 0o004, 0o002, 0o001)

	userDeny := (group | everyone) &^ user
	groupDeny := everyone &^ group
	userAllowFirst := groupDeny &^ userDeny

	var acl []aclEntry
	add := func(tag, perm uint32, typ uint16) {
		acl = append(acl, aclEntry{Tag: tag, ID: aclUndefinedID, Perm: perm, Type: typ})
	}
	if userAllowFirst != 0 {
		add(aclUserObj, userAllowFirst, aclEntryTypeAllow)
	}
	if userDeny != 0 {
		add(aclUserObj, userDeny, aclEntryTypeDeny)
	}
	if groupDeny != 0 {
		add(aclGroupObj, groupDeny, aclEntryTypeDeny)
	}
	add(aclUserObj, user, aclEntryTypeAllow)
	add(aclGroupObj, group, aclEntryTypeAllow)
	add(aclEveryone, everyone, aclEntryTypeAllow)
	return acl
}

// nfs4IsTrivial tells whether an NFSv4 ACL says no more than the mode it
// implies, as acl_is_trivial_np(3) does, so that the mode alone restores it.
// (An ACL in the older "canonical six" form counts as non-trivial here, and is
// kept: that costs a little space, never metadata.)
func nfs4IsTrivial(acl []aclEntry) bool {
	if len(acl) > 6 {
		return false
	}
	// owner@, group@ and everyone@ carry no id; don't let a file system's
	// choice of filler for it matter.
	special := slices.Clone(acl)
	for i := range special {
		switch special[i].Tag {
		case aclUserObj, aclGroupObj, aclEveryone:
			special[i].ID = aclUndefinedID
		}
	}
	return slices.Equal(special, nfs4TrivialACL(nfs4ModeFromACL(acl)))
}

// posixOwningGroupPerms returns the permissions a POSIX.1e access ACL gives
// the file's owning group: its group_obj entry, limited by the mask.  With an
// ACL, a file's mode holds the mask in its group bits, which is wider than
// this whenever a named user or group has more access than the owning group.
func posixOwningGroupPerms(acl []aclEntry) (perms uint32, ok bool) {
	mask := uint32(aclPOSIXPerms)
	for _, e := range acl {
		switch e.Tag {
		case aclGroupObj:
			perms, ok = e.Perm&aclPOSIXPerms, true
		case aclMask:
			mask = e.Perm & aclPOSIXPerms
		}
	}
	return perms & mask, ok
}
