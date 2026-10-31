package fs

import (
	"fmt"
	"strconv"
	"strings"
)

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
// numeric, as the file's owner and group are, but an ACE for a SID (an
// ephemeral id, aceNamesSID) has the SID too, which a restore maps to the id
// it has there.  Readers that don't know "sid" ignore it.
type aceEntry struct {
	Who   uint32 `json:"who"`
	Mask  uint32 `json:"mask"`
	Flags uint16 `json:"flags,omitempty"`
	Type  uint16 `json:"type,omitempty"`
	SID   string `json:"sid,omitempty"`
}

const (
	// illumosMaxUID is MAXUID: the uids and gids above it are ephemeral,
	// those idmap(8) hands out for SIDs (Windows users and groups, as the
	// SMB server and ZFS keep them), and only for as long as it runs: after
	// a reboot the same SID can get another one.
	illumosMaxUID = 2147483647
	// illumosNobody is UID_NOBODY and GID_NOBODY, what an id idmap can't
	// map becomes.
	illumosNobody = 60001
)

// aceNamesSID tells whether e is for a user or group (ACE_IDENTIFIER_GROUP)
// that is a SID: not owner@, group@ or everyone@, and an ephemeral id.
func aceNamesSID(e aceEntry) bool {
	return e.Flags&(aceOwner|aceGroup|aceEveryone) == 0 && e.Who > illumosMaxUID
}

// splitSID splits a SID, such as S-1-5-21-1111-2222-3333-1001, at its last
// dash into what idmap calls its prefix and its RID.
func splitSID(sid string) (string, uint32, error) {
	i := strings.LastIndexByte(sid, '-')
	if i <= 0 || !strings.HasPrefix(sid, "S-") {
		return "", 0, fmt.Errorf("malformed SID %q", sid)
	}
	rid, err := strconv.ParseUint(sid[i+1:], 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("malformed SID %q", sid)
	}
	return sid[:i], uint32(rid), nil
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
