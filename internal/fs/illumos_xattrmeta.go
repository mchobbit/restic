package fs

import "time"

// What illumos keeps of each extended attribute besides its value: an
// attribute is a file of its own (runat(1), fsattr(5)), with an owner, a
// group, a mode, times and an ACL, which illumos.xattr_meta holds by the
// attribute's name.  Without it a restored attribute belongs to whoever ran
// the restore, with mode 0600 and the time of the restore.  Plain Go, so
// that it is tested on any OS; the file access is in xattrmeta_illumos.go.

// xattrMeta is the metadata of one attribute file.  The times are in UTC, in
// the form of a node's mtime; the ACL is there only when it says more than
// the mode (aceIsTrivial), as illumos.acl.
type xattrMeta struct {
	UID   uint32     `json:"uid"`
	GID   uint32     `json:"gid"`
	Mode  uint32     `json:"mode"` // the permission bits, st_mode & 07777
	MTime time.Time  `json:"mtime"`
	ATime time.Time  `json:"atime"`
	ACL   []aceEntry `json:"acl,omitempty"`
}
