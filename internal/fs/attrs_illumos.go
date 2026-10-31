//go:build illumos

package fs

import (
	"fmt"
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// illumos metadata that restic otherwise misses: the ACLs ZFS keeps
// (NFSv4-style ACEs).  Without its ACL a file comes back with its mode alone,
// which loses named users and groups and, worse, deny entries: access the
// ACL denied can come back allowed.  Also the SIDs of owners and ACL entries
// (idmap_illumos.go), the owner, mode, times and ACL of extended attributes
// (xattrmeta_illumos.go) and ZFS project IDs (project_illumos.go).

// acl(2) and facl(2) are libc calls on illumos, reached the way x/sys/unix
// reaches libc.

//go:cgo_import_dynamic libc_acl acl "libc.so"
//go:linkname procacl libc_acl
//go:cgo_import_dynamic libc_facl facl "libc.so"
//go:linkname procfacl libc_facl

var procacl, procfacl uintptr

// sysvicall6 is in sysvicall_illumos_amd64.s.
func sysvicall6(trap, nargs, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

const (
	aceGetACL    = 4 // ACE_GETACL
	aceSetACL    = 5 // ACE_SETACL
	aceGetACLCnt = 6 // ACE_GETACLCNT
)

// rawACE is ace_t from <sys/acl.h>.
type rawACE struct {
	who   uint32
	mask  uint32
	flags uint16
	typ   uint16
}

func acl(path string, cmd int, n int, buf *rawACE) (int, error) {
	p, err := unix.BytePtrFromString(path)
	if err != nil {
		return -1, err
	}
	r, _, errno := sysvicall6(uintptr(unsafe.Pointer(&procacl)), 4,
		uintptr(unsafe.Pointer(p)), uintptr(cmd), uintptr(n), uintptr(unsafe.Pointer(buf)), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(r), nil
}

func facl(fd int, cmd int, n int, buf *rawACE) (int, error) {
	r, _, errno := sysvicall6(uintptr(unsafe.Pointer(&procfacl)), 4,
		uintptr(fd), uintptr(cmd), uintptr(n), uintptr(unsafe.Pointer(buf)), 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(r), nil
}

// aclCall is acl(2) on a path or facl(2) on a descriptor, op its name.
type aclCall struct {
	op, path string
	call     func(cmd int, n int, buf *rawACE) (int, error)
}

func pathACL(path string) aclCall {
	return aclCall{"acl", path, func(cmd int, n int, buf *rawACE) (int, error) {
		return acl(path, cmd, n, buf)
	}}
}

// fdACL is facl(2) on fd, the file open as path.
func fdACL(fd int, path string) aclCall {
	return aclCall{"facl", path, func(cmd int, n int, buf *rawACE) (int, error) {
		return facl(fd, cmd, n, buf)
	}}
}

// getACE reads the ACEs of the file at path; acl(2) follows symlinks, which
// have no ACL of their own anyway.
func getACE(path string) ([]aceEntry, error) {
	return pathACL(path).get()
}

func (a aclCall) get() ([]aceEntry, error) {
	n, err := a.call(aceGetACLCnt, 0, nil)
	if err != nil {
		return nil, &os.PathError{Op: a.op + " ACE_GETACLCNT", Path: a.path, Err: err}
	}
	if n <= 0 {
		return nil, nil
	}
	raw := make([]rawACE, n)
	n, err = a.call(aceGetACL, len(raw), &raw[0])
	if err != nil {
		return nil, &os.PathError{Op: a.op + " ACE_GETACL", Path: a.path, Err: err}
	}
	entries := make([]aceEntry, min(n, len(raw)))
	for i := range entries {
		r := raw[i]
		entries[i] = aceEntry{Who: r.who, Mask: r.mask, Flags: r.flags, Type: r.typ}
	}
	return entries, nil
}

func setACE(path string, entries []aceEntry) error {
	return pathACL(path).set(entries)
}

func (a aclCall) set(entries []aceEntry) error {
	if len(entries) == 0 {
		return nil
	}
	raw := make([]rawACE, len(entries))
	for i, e := range entries {
		raw[i] = rawACE{who: e.Who, mask: e.Mask, flags: e.Flags, typ: e.Type}
	}
	if _, err := a.call(aceSetACL, len(raw), &raw[0]); err != nil {
		return &os.PathError{Op: a.op + " ACE_SETACL", Path: a.path, Err: err}
	}
	return nil
}

// unsupported tells errors that mean the file system or the user can't have
// this kind of metadata, rather than that reading it failed.
func unsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}

// nodeFillOSAttributes records the SIDs of an owner and group that are
// SIDs, and of a regular file or directory its ACL, the metadata of its
// extended attributes and its ZFS project.
func nodeFillOSAttributes(node *data.Node, path string, fi *ExtendedFileInfo) error {
	var sids sidNamer
	if node.UID > illumosMaxUID {
		if sid := sids.sid(node.UID, false); sid != "" {
			_ = node.SetGenericAttribute(data.TypeIllumosOwnerSID, sid)
		}
	}
	if node.GID > illumosMaxUID {
		if sid := sids.sid(node.GID, true); sid != "" {
			_ = node.SetGenericAttribute(data.TypeIllumosGroupSID, sid)
		}
	}
	if node.Type == data.NodeTypeSymlink {
		return sids.report(path)
	}
	err := nodeFillACL(node, path, &sids)
	nodeFillXattrMeta(node, path, &sids)
	nodeFillZFSProject(node, path, fi)
	return errors.Join(err, sids.report(path))
}

func nodeFillACL(node *data.Node, path string, sids *sidNamer) error {
	entries, err := getACE(path)
	if err != nil {
		if unsupported(err) {
			debug.Log("no ACL for %v: %v", path, err)
			return nil
		}
		return err
	}
	if len(entries) == 0 || aceIsTrivial(entries) {
		return nil
	}
	sids.aces(entries)
	return node.SetGenericAttribute(data.TypeIllumosACL, entries)
}

// sidNamer finds the SIDs of a node's ephemeral ids, keeping idmap's first
// failure.
type sidNamer struct {
	err error
}

// sidsLost is set once a backup has reported a SID that idmap didn't give.
var sidsLost atomic.Bool

// sid returns the SID of the ephemeral uid id (gid, if group), or "" when
// idmap has none: idmapd isn't running, say, though then stat(2) already
// gives nobody (60001) for every ephemeral id, which can't be helped.
func (s *sidNamer) sid(id uint32, group bool) string {
	sid, err := sidOfID(id, group)
	if err != nil && s.err == nil {
		s.err = err
	}
	return sid
}

// aces gives each of entries for a SID (aceNamesSID) that SID.
func (s *sidNamer) aces(entries []aceEntry) {
	for i, e := range entries {
		if aceNamesSID(e) {
			entries[i].SID = s.sid(e.Who, e.Flags&aceIdentifierGroup != 0)
		}
	}
}

// report returns idmap's failure for the file at path, the first time in
// the backup: the number alone is kept, which a restore after a reboot may
// give to another SID.
func (s *sidNamer) report(path string) error {
	if s.err == nil {
		return nil
	}
	if sidsLost.Swap(true) {
		debug.Log("SIDs of %v not kept: %v", path, s.err)
		return nil
	}
	return fmt.Errorf("idmap offline: SID owners not kept: %w", s.err)
}

// nodeRestoreOSAttributes restores what goes before the mode on illumos:
// the owner and group that are SIDs, as ids idmap gives them here, the
// metadata of extended attributes, whose values are restored by now, and
// the ZFS project.
func nodeRestoreOSAttributes(node *data.Node, path string) error {
	return errors.Join(nodeRestoreSIDOwner(node, path), nodeRestoreXattrMeta(node, path),
		nodeRestoreZFSProject(node, path))
}

// nodeRestoreSIDOwner gives the file at path the owner and group its SIDs
// have here, after lchown(2) has given it its numbers, which may now be
// another SID's or none (nobody).  A SID idmap can't map gives nobody, with a
// *SIDWarning, not the number: that may belong to another SID.
func nodeRestoreSIDOwner(node *data.Node, path string) error {
	uid, uerr := sidOwner(node, data.TypeIllumosOwnerSID, false)
	gid, gerr := sidOwner(node, data.TypeIllumosGroupSID, true)
	if uid == -1 && gid == -1 {
		return errors.Join(uerr, gerr)
	}
	if err := unix.Lchown(path, uid, gid); err != nil {
		return errors.Join(&os.PathError{Op: "lchown", Path: path, Err: err}, uerr, gerr)
	}
	return errors.Join(uerr, gerr)
}

// sidOwner returns the uid (gid, if group) that the SID in node's attribute
// attr has here, or -1 when there is none.
func sidOwner(node *data.Node, attr data.GenericAttributeType, group bool) (int, error) {
	var sid string
	ok, err := node.GenericAttribute(attr, &sid)
	if err != nil {
		return -1, &data.MalformedAttributeError{Attr: attr, Err: err}
	}
	if !ok {
		return -1, nil
	}
	id, err := idOfSID(sid, group)
	if err != nil {
		return illumosNobody, &SIDWarning{Attr: attr, SID: sid, Err: err}
	}
	return int(id), nil
}

// acesWithIDs gives each of entries that has a SID the id the SID has here,
// or nobody when idmap can't map it, with a *SIDWarning for attr about the
// first such SID (the restorer counts files, not entries).
func acesWithIDs(attr data.GenericAttributeType, entries []aceEntry) *SIDWarning {
	var warning *SIDWarning
	for i, e := range entries {
		if e.SID == "" {
			continue
		}
		id, err := idOfSID(e.SID, e.Flags&aceIdentifierGroup != 0)
		if err != nil {
			id = illumosNobody
			if warning == nil {
				warning = &SIDWarning{Attr: attr, SID: e.SID, Err: err}
			}
		}
		entries[i].Who = id
	}
	return warning
}

// nodeRestoreACLs restores the ACL.  It runs after the mode is set, since
// chmod(2) rewrites a ZFS ACL.
func nodeRestoreACLs(node *data.Node, path string) error {
	if node.Type == data.NodeTypeSymlink {
		return nil
	}
	var entries []aceEntry
	ok, err := node.GenericAttribute(data.TypeIllumosACL, &entries)
	if !ok || err != nil {
		return err
	}
	if warning := acesWithIDs(data.TypeIllumosACL, entries); warning != nil {
		return errors.Join(setACE(path, entries), warning)
	}
	return setACE(path, entries)
}

// A symlink's own permissions don't matter on illumos.
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
