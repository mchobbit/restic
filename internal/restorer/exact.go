package restorer

// BackOtter: restore --exact, as in rustic-bko (rustic_core's
// commands/restore/exact.rs).
//
// --exact makes a target that is there already (a btrfs snapshot of a tree
// restored before, bko-holt's shared snapshots) exactly the snapshot,
// writing only what differs, so that what is the same stays shared: no
// write reaches an entry that already matches, and none reaches the other
// names of a hard-linked file.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/fs"
)

// selinuxLabel is the extended attribute that holds a file's SELinux label.
const selinuxLabel = "security.selinux"

// breakLinkForExact gives the existing regular file at target, when it has
// other names, a name of its own first (fs.BreakHardLink), so that no write
// or metadata change of the restore reaches them; the second pass makes the
// hard links the snapshot has.  Its restricting flags come off first, where
// the node says what they are, for the rename or removal.
func (res *Restorer) breakLinkForExact(node *data.Node, target, location string) error {
	fi, err := fs.Lstat(target)
	if err != nil || !fi.Mode().IsRegular() || fs.ExtendedStat(fi).Links <= 1 {
		return nil
	}
	res.clearRestrictingFlags(node, target)
	cloned, err := fs.BreakHardLink(target)
	if err != nil {
		return res.sanitizeError(location, fmt.Errorf("its hard link not broken: %w", err))
	}
	debug.Log("hard link of %v broken, cloned: %v", target, cloned)
	return nil
}

// keptSpecial tells whether target, under --exact, already is the special
// file node (a symbolic link to the same target, a FIFO, the same device),
// which then stays as it is, and so stays shared.
func (res *Restorer) keptSpecial(node *data.Node, target string) bool {
	if !res.opts.Exact {
		return false
	}
	fi, err := fs.Lstat(target)
	if err != nil {
		return false
	}
	mode := fi.Mode()
	switch node.Type {
	case data.NodeTypeSymlink:
		if mode&os.ModeSymlink == 0 {
			return false
		}
		link, err := os.Readlink(target)
		return err == nil && link == node.LinkTarget
	case data.NodeTypeFifo:
		return mode&os.ModeNamedPipe != 0
	case data.NodeTypeDev:
		return mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0 && fs.ExtendedStat(fi).Device == node.Device
	case data.NodeTypeCharDev:
		return mode&os.ModeCharDevice != 0 && fs.ExtendedStat(fi).Device == node.Device
	}
	return false
}

// sameFile tells whether a and b are the same file.
func sameFile(a, b string) bool {
	afi, err := fs.Lstat(a)
	if err != nil {
		return false
	}
	bfi, err := fs.Lstat(b)
	return err == nil && os.SameFile(afi, bfi)
}

// metadataMatches tells whether what is at target already has node's
// metadata, under --exact, which then leaves it untouched: setting it again
// would change its inode (the change time), which a btrfs snapshot then no
// longer shares.  It matches when what a backup would record for it now
// (nodeNow) is node, but for what a restore doesn't set (sameMetadata).
// With --ownership-by-name as root it never matches: the IDs node's names
// have here may differ from its own.  Anything that can't be read doesn't
// match.
func (res *Restorer) metadataMatches(node *data.Node, target string) bool {
	owner := os.Geteuid() == 0
	if owner && res.opts.OwnershipByName {
		return false
	}
	now, ok := nodeNow(target)
	return ok && sameMetadata(now, node, owner, res.XattrSelectFilter)
}

// nodeNow returns what a backup with real access times would record for
// what is at target now, if it can all be read.
func nodeNow(target string) (*data.Node, bool) {
	f, err := fs.Local{}.OpenFile(target, fs.O_NOFOLLOW, true)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	complete := true
	node, err := f.ToNode(false, func(string, ...any) { complete = false })
	if err != nil || node == nil || !complete {
		debug.Log("metadata of %v not read whole: %v", target, err)
		return nil, false
	}
	return node, true
}

// sameMetadata tells whether now, what a backup would record for a file,
// has node's metadata, as a restore sets it: its type, mode, times (the
// mtime in place of a missing atime; none when node has no mtime), owner's
// IDs when owner, size (a regular file's), symlink target or device, those
// extended attributes selected for the restore, and generic attributes.
// Not what a restore doesn't set: the change time, the inode, the device,
// the link count, the owner's names, the contents (verified apart).
func sameMetadata(now, node *data.Node, owner bool, xattrSelected func(string) bool) bool {
	if now.Type != node.Type || now.Mode != node.Mode {
		return false
	}
	if owner && (now.UID != node.UID || now.GID != node.GID) {
		return false
	}
	if !node.ModTime.IsZero() {
		atime := node.AccessTime
		if atime.IsZero() {
			atime = node.ModTime
		}
		if !now.ModTime.Equal(node.ModTime) || !now.AccessTime.Equal(atime) {
			return false
		}
	}
	switch node.Type {
	case data.NodeTypeFile:
		if now.Size != node.Size {
			return false
		}
	case data.NodeTypeSymlink:
		if now.LinkTarget != node.LinkTarget {
			return false
		}
	case data.NodeTypeDev, data.NodeTypeCharDev:
		if now.Device != node.Device {
			return false
		}
	}
	return sameXattrs(now.ExtendedAttributes, node.ExtendedAttributes, xattrSelected) &&
		sameGenericAttributes(now.GenericAttributes, node.GenericAttributes)
}

// sameXattrs tells whether a and b hold the same extended attributes of
// those selected, in any order.
func sameXattrs(a, b []data.ExtendedAttribute, selected func(string) bool) bool {
	pick := func(attrs []data.ExtendedAttribute) []data.ExtendedAttribute {
		picked := make([]data.ExtendedAttribute, 0, len(attrs))
		for _, attr := range attrs {
			if selected(attr.Name) {
				picked = append(picked, attr)
			}
		}
		slices.SortFunc(picked, func(x, y data.ExtendedAttribute) int { return strings.Compare(x.Name, y.Name) })
		return picked
	}
	return slices.EqualFunc(pick(a), pick(b), func(x, y data.ExtendedAttribute) bool {
		return x.Name == y.Name && bytes.Equal(x.Value, y.Value)
	})
}

// sameGenericAttributes tells whether a and b hold the same generic
// attributes, with the same values, however their JSON is spaced.
func sameGenericAttributes(a, b map[data.GenericAttributeType]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for name, va := range a {
		vb, ok := b[name]
		if !ok {
			return false
		}
		if bytes.Equal(va, vb) {
			continue
		}
		var ca, cb bytes.Buffer
		if json.Compact(&ca, va) != nil || json.Compact(&cb, vb) != nil || !bytes.Equal(ca.Bytes(), cb.Bytes()) {
			return false
		}
	}
	return true
}

// labelError is RemoveSELinuxLabel's error under --exact, a warning once,
// with the number of files.
type labelError struct {
	err error
}

func (e *labelError) Error() string {
	return e.err.Error()
}

func (e *labelError) Unwrap() error {
	return e.err
}

// removeLabel removes, under --exact, the SELinux label of target where its
// node has none and the label is selected for the restore: a btrfs snapshot
// made exact inherits its base's labels, and the snapshot restored may have
// none (Tumbleweed's snapper snapshots).  Elsewhere a label stays, as
// before.
func (res *Restorer) removeLabel(node *data.Node, target, location string) {
	if !res.opts.Exact || !res.XattrSelectFilter(selinuxLabel) {
		return
	}
	for _, attr := range node.ExtendedAttributes {
		if attr.Name == selinuxLabel {
			return
		}
	}
	if err := fs.RemoveSELinuxLabel(target); err != nil {
		res.warnAbout(location, &labelError{err})
	}
}
