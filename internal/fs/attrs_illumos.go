//go:build illumos

package fs

import (
	"os"
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
// ACL denied can come back allowed.

// acl(2) is a libc call on illumos, reached the way x/sys/unix reaches libc.

//go:cgo_import_dynamic libc_acl acl "libc.so"
//go:linkname procacl libc_acl

var procacl uintptr

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

// getACE reads the ACEs of the file at path; acl(2) follows symlinks, which
// have no ACL of their own anyway.
func getACE(path string) ([]aceEntry, error) {
	n, err := acl(path, aceGetACLCnt, 0, nil)
	if err != nil {
		return nil, &os.PathError{Op: "acl ACE_GETACLCNT", Path: path, Err: err}
	}
	if n <= 0 {
		return nil, nil
	}
	raw := make([]rawACE, n)
	n, err = acl(path, aceGetACL, len(raw), &raw[0])
	if err != nil {
		return nil, &os.PathError{Op: "acl ACE_GETACL", Path: path, Err: err}
	}
	entries := make([]aceEntry, min(n, len(raw)))
	for i := range entries {
		r := raw[i]
		entries[i] = aceEntry{Who: r.who, Mask: r.mask, Flags: r.flags, Type: r.typ}
	}
	return entries, nil
}

func setACE(path string, entries []aceEntry) error {
	if len(entries) == 0 {
		return nil
	}
	raw := make([]rawACE, len(entries))
	for i, e := range entries {
		raw[i] = rawACE{who: e.Who, mask: e.Mask, flags: e.Flags, typ: e.Type}
	}
	if _, err := acl(path, aceSetACL, len(raw), &raw[0]); err != nil {
		return &os.PathError{Op: "acl ACE_SETACL", Path: path, Err: err}
	}
	return nil
}

// unsupported tells errors that mean the file system or the user can't have
// this kind of metadata, rather than that reading it failed.
func unsupported(err error) bool {
	return errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.ENOTSUP) ||
		errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES)
}

func nodeFillOSAttributes(node *data.Node, path string, _ *ExtendedFileInfo) error {
	if node.Type == data.NodeTypeSymlink {
		return nil
	}
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
	return node.SetGenericAttribute(data.TypeIllumosACL, entries)
}

// nodeRestoreOSAttributes has nothing to restore before the mode on illumos.
func nodeRestoreOSAttributes(_ *data.Node, _ string) error {
	return nil
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
	return setACE(path, entries)
}

// A symlink's own permissions don't matter on illumos.
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
