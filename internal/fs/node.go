package fs

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// nodeFromFileInfo returns a new node from the given path and FileInfo. It
// returns the first error that is encountered, together with a node.
func nodeFromFileInfo(path string, fi *ExtendedFileInfo, ignoreXattrListError bool, warnf func(format string, args ...any)) (*data.Node, error) {
	node := buildBasicNode(path, fi)

	if err := nodeFillExtendedStat(node, path, fi); err != nil {
		return node, err
	}

	err := nodeFillGenericAttributes(node, path, fi)
	err = errors.Join(err, nodeFillExtendedAttributes(node, path, ignoreXattrListError, warnf))
	return node, err
}

func buildBasicNode(path string, fi *ExtendedFileInfo) *data.Node {
	mask := os.ModePerm | os.ModeType | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	node := &data.Node{
		Path:    path,
		Name:    fi.Name,
		Mode:    fi.Mode & mask,
		ModTime: fi.ModTime,
	}

	node.Type = nodeTypeFromFileInfo(fi.Mode)
	if node.Type == data.NodeTypeFile {
		node.Size = uint64(fi.Size)
	}
	return node
}

func nodeTypeFromFileInfo(mode os.FileMode) data.NodeType {
	switch mode & os.ModeType {
	case 0:
		return data.NodeTypeFile
	case os.ModeDir:
		return data.NodeTypeDir
	case os.ModeSymlink:
		return data.NodeTypeSymlink
	case os.ModeDevice | os.ModeCharDevice:
		return data.NodeTypeCharDev
	case os.ModeDevice:
		return data.NodeTypeDev
	case os.ModeNamedPipe:
		return data.NodeTypeFifo
	case os.ModeSocket:
		return data.NodeTypeSocket
	case os.ModeIrregular:
		return data.NodeTypeIrregular
	}

	return data.NodeTypeInvalid
}

func nodeFillExtendedStat(node *data.Node, path string, stat *ExtendedFileInfo) error {
	node.Inode = stat.Inode
	node.DeviceID = stat.DeviceID
	node.ChangeTime = stat.ChangeTime
	node.AccessTime = stat.AccessTime

	node.UID = stat.UID
	node.GID = stat.GID
	node.User = lookupUsername(stat.UID)
	node.Group = lookupGroup(stat.GID)

	switch node.Type {
	case data.NodeTypeFile:
		node.Size = uint64(stat.Size)
		node.Links = stat.Links
	case data.NodeTypeDir:
	case data.NodeTypeSymlink:
		var err error
		node.LinkTarget, err = os.Readlink(fixpath(path))
		node.Links = stat.Links
		if err != nil {
			return errors.WithStack(err)
		}
	case data.NodeTypeDev:
		node.Device = stat.Device
		node.Links = stat.Links
	case data.NodeTypeCharDev:
		node.Device = stat.Device
		node.Links = stat.Links
	case data.NodeTypeFifo:
	case data.NodeTypeSocket:
	default:
		return errors.Errorf("unsupported file type %q", node.Type)
	}
	return nil
}

var (
	uidLookupCache      = make(map[uint32]string)
	uidLookupCacheMutex = sync.RWMutex{}
)

// Cached user name lookup by uid. Returns "" when no name can be found.
func lookupUsername(uid uint32) string {
	uidLookupCacheMutex.RLock()
	username, ok := uidLookupCache[uid]
	uidLookupCacheMutex.RUnlock()

	if ok {
		return username
	}

	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err == nil {
		username = u.Username
	}

	uidLookupCacheMutex.Lock()
	uidLookupCache[uid] = username
	uidLookupCacheMutex.Unlock()

	return username
}

// userNameLookup is a cached uid or gid lookup.
type userNameLookup struct {
	id uint32
	ok bool
}

var (
	userNameLookupCache      = make(map[string]userNameLookup)
	userNameLookupCacheMutex = sync.RWMutex{}
)

// Cached uid lookup by user name, and whether there is one: not for an
// empty name, or a user unknown here.
//
//nolint:revive // capitalization is correct as is
func lookupUid(userName string) (uint32, bool) {
	if userName == "" {
		return 0, false
	}
	userNameLookupCacheMutex.RLock()
	cached, ok := userNameLookupCache[userName]
	userNameLookupCacheMutex.RUnlock()

	if ok {
		return cached.id, cached.ok
	}

	u, err := user.Lookup(userName)
	if err == nil {
		var s int
		s, err = strconv.Atoi(u.Uid)
		if err == nil {
			cached = userNameLookup{uint32(s), true}
		}
	}

	userNameLookupCacheMutex.Lock()
	userNameLookupCache[userName] = cached
	userNameLookupCacheMutex.Unlock()

	return cached.id, cached.ok
}

var (
	gidLookupCache      = make(map[uint32]string)
	gidLookupCacheMutex = sync.RWMutex{}
)

// Cached group name lookup by gid. Returns "" when no name can be found.
func lookupGroup(gid uint32) string {
	gidLookupCacheMutex.RLock()
	group, ok := gidLookupCache[gid]
	gidLookupCacheMutex.RUnlock()

	if ok {
		return group
	}

	g, err := user.LookupGroupId(strconv.Itoa(int(gid)))
	if err == nil {
		group = g.Name
	}

	gidLookupCacheMutex.Lock()
	gidLookupCache[gid] = group
	gidLookupCacheMutex.Unlock()

	return group
}

var (
	groupNameLookupCache      = make(map[string]userNameLookup)
	groupNameLookupCacheMutex = sync.RWMutex{}
)

// Cached gid lookup by group name, and whether there is one: not for an
// empty name, or a group unknown here.
func lookupGid(groupName string) (uint32, bool) {
	if groupName == "" {
		return 0, false
	}
	groupNameLookupCacheMutex.RLock()
	cached, ok := groupNameLookupCache[groupName]
	groupNameLookupCacheMutex.RUnlock()

	if ok {
		return cached.id, cached.ok
	}

	g, err := user.LookupGroup(groupName)
	if err == nil {
		var s int
		s, err = strconv.Atoi(g.Gid)
		if err == nil {
			cached = userNameLookup{uint32(s), true}
		}
	}

	groupNameLookupCacheMutex.Lock()
	groupNameLookupCache[groupName] = cached
	groupNameLookupCacheMutex.Unlock()

	return cached.id, cached.ok
}

// NodeCreateAt creates the node at the given path but does NOT restore node meta data.
func NodeCreateAt(node *data.Node, path string) (err error) {
	debug.Log("create node %v at %v", node.Name, path)

	switch node.Type {
	case data.NodeTypeDir:
		err = nodeCreateDirAt(node, path)
	case data.NodeTypeFile:
		err = nodeCreateFileAt(path)
	case data.NodeTypeSymlink:
		err = nodeCreateSymlinkAt(node, path)
	case data.NodeTypeDev:
		err = nodeCreateDevAt(node, path)
	case data.NodeTypeCharDev:
		err = nodeCreateCharDevAt(node, path)
	case data.NodeTypeFifo:
		err = nodeCreateFifoAt(path)
	case data.NodeTypeSocket:
		err = nil
	default:
		err = errors.Errorf("filetype %q not implemented", node.Type)
	}

	return err
}

func nodeCreateDirAt(node *data.Node, path string) error {
	err := os.Mkdir(fixpath(path), node.Mode)
	if err != nil && !os.IsExist(err) {
		return errors.WithStack(err)
	}

	return nil
}

func nodeCreateFileAt(path string) error {
	f, err := OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return errors.WithStack(err)
	}

	if err := f.Close(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func nodeCreateSymlinkAt(node *data.Node, path string) error {
	if err := os.Symlink(node.LinkTarget, fixpath(path)); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

func nodeCreateDevAt(node *data.Node, path string) error {
	return mknod(path, syscall.S_IFBLK|0600, node.Device)
}

func nodeCreateCharDevAt(node *data.Node, path string) error {
	return mknod(path, syscall.S_IFCHR|0600, node.Device)
}

func nodeCreateFifoAt(path string) error {
	return mkfifo(path, 0600)
}

func mkfifo(path string, mode uint32) (err error) {
	return mknod(path, mode|syscall.S_IFIFO, 0)
}

// NodeRestoreMetadata restores node metadata
func NodeRestoreMetadata(node *data.Node, path string, warn func(msg string), xattrSelectFilter func(xattrName string) bool, ownershipByName bool) error {
	err := nodeRestoreMetadata(node, path, warn, xattrSelectFilter, ownershipByName)
	if err != nil {
		// It is common to have permission errors for folders like /home
		// unless you're running as root, so ignore those.
		rest, warnings := SplitWarnings(err)
		if os.Geteuid() > 0 && errors.Is(rest, os.ErrPermission) {
			debug.Log("not running as root, ignoring permission error for %v: %v",
				path, err)
			return errors.Join(warnings...)
		}
		debug.Log("restoreMetadata(%s) error %v", path, err)
	}

	return err
}

func nodeRestoreMetadata(node *data.Node, path string, warn func(msg string), xattrSelectFilter func(xattrName string) bool, ownershipByName bool) error {
	var firsterr error

	if err := lchown(path, node, ownershipByName); err != nil {
		firsterr = errors.WithStack(err)
	}

	if err := nodeRestoreExtendedAttributes(node, path, xattrSelectFilter); err != nil {
		debug.Log("error restoring extended attributes for %v: %v", path, err)
		if firsterr == nil {
			firsterr = err
		}
	}

	// BackOtter: the warnings among the generic attributes' errors (a file
	// system without project IDs, a malformed attribute) are all returned,
	// joined after the first error, for the restorer to warn about.
	var warnings []error
	if err := nodeRestoreGenericAttributes(node, path, warn); err != nil {
		debug.Log("error restoring generic attributes for %v: %v", path, err)
		err, warnings = SplitWarnings(err)
		if err != nil && firsterr == nil {
			firsterr = err
		}
	}

	if err := nodeRestoreTimestamps(node, path); err != nil {
		debug.Log("error restoring timestamps for %v: %v", path, err)
		err, w := SplitWarnings(err)
		warnings = append(warnings, w...)
		if err != nil && firsterr == nil {
			firsterr = err
		}
	}

	// Moving RestoreTimestamps and restoreExtendedAttributes calls above as for readonly files in windows
	// calling Chmod below will no longer allow any modifications to be made on the file and the
	// calls above would fail.
	if node.Type != data.NodeTypeSymlink {
		if err := chmod(path, node.Mode); err != nil {
			if firsterr == nil {
				firsterr = errors.WithStack(err)
			}
		}
	} else if err := nodeRestoreSymlinkMode(node, path); err != nil {
		debug.Log("error restoring the mode of symlink %v: %v", path, err)
		if firsterr == nil {
			firsterr = err
		}
	}

	// After the mode: chmod rewrites an NFSv4 ACL, and setting an ACL sets
	// the mode's permission bits from its entries.  Where setting an ACL
	// sets the mtime too (NetBSD), it sets the times again after them.
	if err := nodeRestoreACLs(node, path); err != nil {
		debug.Log("error restoring ACLs for %v: %v", path, err)
		err, w := SplitWarnings(err)
		warnings = append(warnings, w...)
		if err != nil && firsterr == nil {
			firsterr = err
		}
	}

	if len(warnings) == 0 {
		return firsterr
	}
	return errors.Join(append([]error{firsterr}, warnings...)...)
}

func nodeRestoreTimestamps(node *data.Node, path string) error {
	atime := node.AccessTime.UnixNano()
	mtime := node.ModTime.UnixNano()

	// BackOtter: the birth time goes just before the atime and mtime: on
	// FreeBSD and NetBSD it is set as an earlier mtime, which they replace
	// (birthtime.go).  Its errors are reported with theirs, and don't keep
	// them from being set.
	birthErr := nodeRestoreBirthTime(node, fixpath(path))
	if err := utimesNano(fixpath(path), atime, mtime, node.Type); err != nil {
		return errors.Join(birthErr, fmt.Errorf("failed to restore timestamp of %q: %w", path, err))
	}
	return birthErr
}
