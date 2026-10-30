//go:build freebsd

package fs

import (
	"maps"
	"os"
	"slices"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// FreeBSD metadata that restic's xattr support (the user namespace only)
// misses: extended attributes in the system namespace, and ACLs, POSIX.1e
// (UFS mounted with "acls") or NFSv4 (ZFS, UFS mounted with "nfsv4acls").
// Without its ACL a file comes back with its mode alone, and a POSIX.1e
// ACL's mode holds the mask in its group bits, so the owning group can end
// up with more access than it had.

const (
	aclTypeAccess  = 2 // ACL_TYPE_ACCESS
	aclTypeDefault = 3 // ACL_TYPE_DEFAULT
	aclTypeNFS4    = 4 // ACL_TYPE_NFS4

	pcACLExtended = 59 // _PC_ACL_EXTENDED
	pcACLNFS4     = 64 // _PC_ACL_NFS4
)

// ufsACLExtattrs are the system extended attributes UFS stores ACLs in.  They
// are kept as ACLs instead, and set through the ACL system calls, which check
// them.
var ufsACLExtattrs = map[string]bool{
	"posix1e.acl_access":  true,
	"posix1e.acl_default": true,
	"nfs4.acl":            true,
}

// rawACL is struct acl from <sys/acl.h>, as __acl_get_link(2) and
// __acl_set_link(2) take it.
type rawACL struct {
	maxcnt  uint32
	cnt     uint32
	spare   [4]int32
	entries [aclMaxEntries]struct {
		tag, id, perm    uint32
		entryType, flags uint16
	}
}

func aclSyscall(trap uintptr, path string, typ int, acl *rawACL) error {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := unix.Syscall(trap, uintptr(unsafe.Pointer(p)), uintptr(typ), uintptr(unsafe.Pointer(acl)))
	if errno != 0 {
		return errno
	}
	return nil
}

// getACL reads the ACL of type typ of the file at path, without following a
// symlink.
func getACL(path string, typ int) ([]aclEntry, error) {
	raw := rawACL{maxcnt: aclMaxEntries}
	if err := aclSyscall(unix.SYS___ACL_GET_LINK, path, typ, &raw); err != nil {
		return nil, &os.PathError{Op: "__acl_get_link", Path: path, Err: err}
	}
	if raw.cnt > aclMaxEntries {
		return nil, errors.Errorf("%s: ACL with %d entries", path, raw.cnt)
	}
	acl := make([]aclEntry, raw.cnt)
	for i := range acl {
		e := raw.entries[i]
		acl[i] = aclEntry{Tag: e.tag, ID: e.id, Perm: e.perm, Type: e.entryType, Flags: e.flags}
	}
	return acl, nil
}

// setACL sets the ACL of type typ of the file at path.
func setACL(path string, typ int, acl []aclEntry) error {
	if len(acl) > aclMaxEntries {
		return errors.Errorf("%s: ACL with %d entries", path, len(acl))
	}
	raw := rawACL{maxcnt: aclMaxEntries, cnt: uint32(len(acl))}
	for i, e := range acl {
		r := &raw.entries[i]
		r.tag, r.id, r.perm, r.entryType, r.flags = e.Tag, e.ID, e.Perm, e.Type, e.Flags
	}
	if err := aclSyscall(unix.SYS___ACL_SET_LINK, path, typ, &raw); err != nil {
		return &os.PathError{Op: "__acl_set_link", Path: path, Err: err}
	}
	return nil
}

func lpathconf(path string, name int) (int, error) {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, errno := unix.Syscall(unix.SYS_LPATHCONF, uintptr(unsafe.Pointer(p)), uintptr(name), 0)
	if errno != 0 {
		return 0, errno
	}
	return int(r), nil
}

// unsupported tells errors that mean the file system or the user can't have
// this kind of metadata, rather than that reading it failed.
func unsupported(err error) bool {
	return errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) ||
		errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}

// listSystemExtattrs lists the names of the system extended attributes of
// the file at path, without following a symlink.
func listSystemExtattrs(path string) ([]string, error) {
	size, err := unix.ExtattrListLink(path, unix.EXTATTR_NAMESPACE_SYSTEM, 0, 0)
	if err != nil || size == 0 {
		return nil, err
	}
	buf := make([]byte, size)
	size, err = unix.ExtattrListLink(path, unix.EXTATTR_NAMESPACE_SYSTEM, uintptr(unsafe.Pointer(&buf[0])), len(buf))
	if err != nil {
		return nil, err
	}
	// Each name follows a byte holding its length.
	var names []string
	for i := 0; i < size && i < len(buf); {
		n := int(buf[i])
		if i+1+n > len(buf) {
			break
		}
		names = append(names, string(buf[i+1:i+1+n]))
		i += 1 + n
	}
	return names, nil
}

func getSystemExtattr(path, name string) ([]byte, error) {
	size, err := unix.ExtattrGetLink(path, unix.EXTATTR_NAMESPACE_SYSTEM, name, 0, 0)
	if err != nil || size == 0 {
		return []byte{}, err
	}
	buf := make([]byte, size)
	size, err = unix.ExtattrGetLink(path, unix.EXTATTR_NAMESPACE_SYSTEM, name, uintptr(unsafe.Pointer(&buf[0])), len(buf))
	if err != nil {
		return nil, err
	}
	return buf[:min(size, len(buf))], nil
}

func setSystemExtattr(path, name string, value []byte) error {
	var p uintptr
	if len(value) > 0 {
		p = uintptr(unsafe.Pointer(&value[0]))
	}
	_, err := unix.ExtattrSetLink(path, unix.EXTATTR_NAMESPACE_SYSTEM, name, p, len(value))
	if err != nil {
		return &os.PathError{Op: "extattr_set_link system." + name, Path: path, Err: err}
	}
	return nil
}

func nodeFillFreeBSDAttributes(node *data.Node, path string, _ *ExtendedFileInfo) error {
	return errors.Join(nodeFillSystemExtattrs(node, path), nodeFillACLs(node, path))
}

func nodeFillSystemExtattrs(node *data.Node, path string) error {
	names, err := listSystemExtattrs(path)
	if err != nil {
		if unsupported(err) {
			// Not root, or no extended attributes on this file system.
			debug.Log("no system extended attributes for %v: %v", path, err)
			return nil
		}
		return &os.PathError{Op: "extattr_list_link system", Path: path, Err: err}
	}
	attrs := make(map[string][]byte)
	for _, name := range names {
		if ufsACLExtattrs[name] {
			continue
		}
		value, err := getSystemExtattr(path, name)
		if errors.Is(err, unix.ENOATTR) {
			continue // removed since the listing
		}
		if err != nil {
			return &os.PathError{Op: "extattr_get_link system." + name, Path: path, Err: err}
		}
		attrs[name] = value
	}
	if len(attrs) == 0 {
		return nil
	}
	return node.SetGenericAttribute(data.TypeFreeBSDSystemExtattrs, attrs)
}

func nodeFillACLs(node *data.Node, path string) error {
	if node.Type == data.NodeTypeSymlink {
		return nil // FreeBSD has no ACLs on symlinks
	}
	if v, err := lpathconf(path, pcACLNFS4); err == nil && v == 1 {
		acl, err := getACL(path, aclTypeNFS4)
		if err != nil {
			return ignoreUnsupported(path, err)
		}
		if nfs4IsTrivial(acl) {
			return nil
		}
		return node.SetGenericAttribute(data.TypeFreeBSDACLNFS4, acl)
	}
	if v, err := lpathconf(path, pcACLExtended); err != nil || v != 1 {
		return nil // no ACLs on this file system
	}
	acl, err := getACL(path, aclTypeAccess)
	if err != nil {
		return ignoreUnsupported(path, err)
	}
	// Three entries are just the mode: user_obj, group_obj and other.
	if len(acl) > 3 {
		if err := node.SetGenericAttribute(data.TypeFreeBSDACLAccess, acl); err != nil {
			return err
		}
	}
	if node.Type != data.NodeTypeDir {
		return nil
	}
	acl, err = getACL(path, aclTypeDefault)
	if err != nil {
		return ignoreUnsupported(path, err)
	}
	if len(acl) == 0 {
		return nil
	}
	return node.SetGenericAttribute(data.TypeFreeBSDACLDefault, acl)
}

func ignoreUnsupported(path string, err error) error {
	if unsupported(err) {
		debug.Log("no ACL for %v: %v", path, err)
		return nil
	}
	return err
}

// nodeRestoreFreeBSDExtattrs restores the system extended attributes.
func nodeRestoreFreeBSDExtattrs(node *data.Node, path string) error {
	var attrs map[string][]byte
	ok, err := node.GenericAttribute(data.TypeFreeBSDSystemExtattrs, &attrs)
	if !ok || err != nil {
		return err
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(attrs)) {
		errs = append(errs, setSystemExtattr(path, name, attrs[name]))
	}
	return errors.Join(errs...)
}

// nodeRestoreSymlinkMode gives a symlink its own permissions back: FreeBSD
// keeps them (lchmod(2)), and ls(1) and mtree(8) show them, though access
// doesn't depend on them.  Without this a restored symlink gets 0755.
func nodeRestoreSymlinkMode(node *data.Node, path string) error {
	err := unix.Fchmodat(unix.AT_FDCWD, path, uint32(node.Mode.Perm()), unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: path, Err: err}
	}
	return nil
}

// nodeRestoreACLs restores the ACLs.  It runs after the mode is set: chmod(2)
// rewrites an NFSv4 ACL (ZFS discards it), and setting an ACL sets the
// mode's permission bits from its entries.
func nodeRestoreACLs(node *data.Node, path string) error {
	if node.Type == data.NodeTypeSymlink {
		return nil
	}
	var errs []error
	restore := func(typ data.GenericAttributeType, aclType int, failed func(acl []aclEntry)) {
		var acl []aclEntry
		ok, err := node.GenericAttribute(typ, &acl)
		if err != nil {
			errs = append(errs, err)
			return
		}
		if !ok {
			return
		}
		if err := setACL(path, aclType, acl); err != nil {
			errs = append(errs, err)
			if failed != nil {
				failed(acl)
			}
		}
	}
	restore(data.TypeFreeBSDACLNFS4, aclTypeNFS4, nil)
	restore(data.TypeFreeBSDACLAccess, aclTypeAccess, func(acl []aclEntry) {
		// The mode restored above has the ACL's mask in its group bits.
		// Without the ACL, they'd give the owning group all that named
		// users and groups had; narrow them to the group's own access.
		if perms, ok := posixOwningGroupPerms(acl); ok {
			mode := node.Mode&^0o070 | os.FileMode(perms<<3)
			if err := chmod(path, mode); err != nil {
				errs = append(errs, errors.WithStack(err))
			}
		}
	})
	restore(data.TypeFreeBSDACLDefault, aclTypeDefault, nil)
	return errors.Join(errs...)
}
