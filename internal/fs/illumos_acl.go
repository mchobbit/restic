package fs

// illumos ACLs, as ZFS keeps them: NFSv4-style ACEs (ace_t in <sys/acl.h>),
// and the rule illumos uses to tell a trivial ACL, one the mode says all of,
// from one worth keeping.  Plain Go, so that it is tested on any OS; the
// library calls are in attrs_illumos.go.

const (
	// a_type
	aceAccessAllowed = 0x0000
	aceAccessDenied  = 0x0001

	// a_flags
	aceFileInherit       = 0x0001
	aceDirectoryInherit  = 0x0002
	aceNoPropagate       = 0x0004
	aceInheritOnly       = 0x0008
	aceIdentifierGroup   = 0x0040
	aceOwner             = 0x1000
	aceGroup             = 0x2000
	aceEveryone          = 0x4000
	aceTypeFlags         = aceOwner | aceGroup | aceEveryone | aceIdentifierGroup
	aceInheritanceFlags  = aceFileInherit | aceDirectoryInherit | aceNoPropagate | aceInheritOnly
	aceWriteData         = 0x00000002
	aceWriteNamedAttrs   = 0x00000010
	aceDeleteChild       = 0x00000040
	aceReadAttributes    = 0x00000080
	aceWriteAttributes   = 0x00000100
	aceDelete            = 0x00010000
	aceReadACL           = 0x00020000
	aceWriteACL          = 0x00040000
	aceWriteOwner        = 0x00080000
	aceOwnerOnlyAllowed  = aceWriteOwner | aceWriteACL | aceWriteAttributes | aceWriteNamedAttrs
	aceNeverDeniedByMode = aceReadACL | aceReadAttributes
)

// aceEntry is one ACE of an illumos ACL (ace_t), as restic keeps it: ids are
// numeric, as the file's owner and group are.
type aceEntry struct {
	Who   uint32 `json:"who"`
	Mask  uint32 `json:"mask"`
	Flags uint16 `json:"flags,omitempty"`
	Type  uint16 `json:"type,omitempty"`
}

// aceIsTrivial is ace_trivial_common() from illumos' acl_common.c: whether an
// ACL says no more than the file's mode, so that the mode alone restores it.
// It's what ls(1) leaves the "+" off for.
func aceIsTrivial(acl []aceEntry) bool {
	for _, e := range acl {
		switch e.Flags & aceTypeFlags {
		case aceOwner, aceGroup | aceIdentifierGroup, aceEveryone:
		default:
			return false // a named user or group
		}
		if e.Flags&aceInheritanceFlags != 0 {
			return false
		}
		// Nobody is denied reading basic attributes or the ACL by a mode.
		if e.Mask&aceNeverDeniedByMode != 0 && e.Type == aceAccessDenied {
			return false
		}
		// A mode never grants delete, nor delete_child without write.
		if e.Mask&aceDelete != 0 {
			return false
		}
		if e.Mask&aceDeleteChild != 0 && e.Mask&aceWriteData == 0 {
			return false
		}
		// Only owner@ is allowed to change the ACL, owner and attributes.
		if e.Type == aceAccessAllowed && e.Flags&aceOwner == 0 && e.Mask&aceOwnerOnlyAllowed != 0 {
			return false
		}
	}
	return true
}
