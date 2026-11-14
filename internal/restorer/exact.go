package restorer

// BackOtter: restore --exact and --clone-from, as in rustic-bko
// (rustic_core's commands/restore/exact.rs).
//
// --exact makes a target that is there already (a btrfs snapshot of a tree
// restored before, bko-holt's shared snapshots) exactly the snapshot,
// writing only what differs, so that what is the same stays shared: no
// write reaches an entry that already matches, and none reaches the other
// names of a hard-linked file.  --clone-from gives it files whose contents
// were restored elsewhere on the same file system, cloned whole instead of
// written.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
)

// selinuxLabel is the extended attribute that holds a file's SELinux label.
const selinuxLabel = "security.selinux"

// CloneSources are files restored elsewhere, by their contents (the blobs a
// snapshot lists for them), which a restore with --exact may clone instead
// of writing (Restorer.CloneSources).
type CloneSources struct {
	byContent map[string]string
}

// contentKey is the key content has in CloneSources.
func contentKey(content restic.IDs) string {
	var b strings.Builder
	b.Grow(len(content) * len(restic.ID{}))
	for _, id := range content {
		_, _ = b.Write(id[:])
	}
	return b.String()
}

// add notes that path holds content.  The first file given for some
// contents is the one cloned.
func (c *CloneSources) add(content restic.IDs, path string) {
	if c.byContent == nil {
		c.byContent = make(map[string]string)
	}
	key := contentKey(content)
	if _, ok := c.byContent[key]; !ok {
		c.byContent[key] = path
	}
}

// Get returns a file that holds content, if there is one.
func (c *CloneSources) Get(content restic.IDs) (string, bool) {
	if c == nil || len(content) == 0 {
		return "", false
	}
	path, ok := c.byContent[contentKey(content)]
	return path, ok
}

// Len returns the number of contents there are files for.
func (c *CloneSources) Len() int {
	if c == nil {
		return 0
	}
	return len(c.byContent)
}

// NewCloneSources returns the regular files of the snapshot's tree tree (a
// directory's), restored at dir, by their contents: those of their size that
// are there now, but none at or below dest, the restore's target, when that
// is inside dir.  dir must hold the tree as restored, unchanged since; the
// target doesn't (a snapper snapshot inside the subvolume cloned from,
// seeded from it until restored): a file there would be cloned onto
// itself, or give another file its seeded contents.  The tree is walked
// depth first, in the order of its names.  As in rustic-bko
// (Repository::clone_sources).
func NewCloneSources(ctx context.Context, repo restic.BlobLoader, tree restic.ID, dir, dest string) (*CloneSources, error) {
	c := &CloneSources{}
	skip, inside := pathBelow(dir, dest)
	if inside && skip == "" {
		return c, nil // the target is dir itself
	}
	var walk func(id restic.ID, rel string) error
	walk = func(id restic.ID, rel string) error {
		tree, err := data.LoadTree(ctx, repo, id)
		if err != nil {
			return err
		}
		for item := range tree {
			if item.Error != nil {
				return item.Error
			}
			node := item.Node
			if node.Name != filepath.Base(filepath.Join(string(filepath.Separator), node.Name)) {
				continue // an invalid name, which the restore refuses too
			}
			nodeRel := filepath.Join(rel, node.Name)
			if inside && fs.HasPathPrefix(skip, nodeRel) {
				continue
			}
			switch {
			case node.Type == data.NodeTypeDir && node.Subtree != nil:
				if err := walk(*node.Subtree, nodeRel); err != nil {
					return err
				}
			case node.Type == data.NodeTypeFile && node.Size > 0 && len(node.Content) > 0:
				// Only files that are there, of their size: not what dir
				// doesn't hold (the paths below other subvolumes' mount
				// points, aliases).
				path := filepath.Join(dir, nodeRel)
				if fi, err := fs.Lstat(path); err == nil && fi.Mode().IsRegular() && uint64(fi.Size()) == node.Size {
					c.add(node.Content, path)
				}
			}
		}
		return nil
	}
	return c, walk(tree, "")
}

// pathBelow returns path's path below dir ("" for dir itself), and whether it
// is there, as both resolve (symbolic links, "..", relative paths) where they
// can, or else as they are written.
func pathBelow(dir, path string) (string, bool) {
	resolve := func(p string) (string, error) {
		p, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	}
	if d, err := resolve(dir); err == nil {
		if p, err := resolve(path); err == nil {
			if rel, ok := relBelow(d, p); ok {
				return rel, true
			}
		}
	}
	return relBelow(filepath.Clean(dir), filepath.Clean(path))
}

// relBelow is pathBelow for clean paths, as they are.
func relBelow(dir, path string) (string, bool) {
	if !fs.HasPathPrefix(dir, path) {
		return "", false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false
	}
	if rel == "." {
		rel = ""
	}
	return rel, true
}

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

// cloneOK tells whether node's contents may come from a clone: not when its
// file takes flags or options before its data (btrfs's no-copy-on-write,
// which a clone refuses; XFS's extent size hint).
func (res *Restorer) cloneOK(node *data.Node, location string) bool {
	if flags, ok := res.nodeFileFlags(node, location); ok && fs.HasFlagsBeforeData(flags) {
		return false
	}
	_, hasXfsOpts := res.nodeXFSOptions(node, location)
	return !hasXfsOpts
}

// cloneFromSources clones the contents of the file node at target, which the
// restore would write, from a file restored elsewhere that has them
// (CloneSources), if any.  cloned tells whether it did; emptied whether a
// clone that failed made or emptied target first, which then holds none of
// what matched.
func (res *Restorer) cloneFromSources(node *data.Node, target, location string) (cloned, emptied bool) {
	if !res.opts.Exact || res.opts.DryRun {
		return false, false
	}
	src, ok := res.CloneSources.Get(node.Content)
	if !ok || !res.cloneOK(node, location) {
		return false, false
	}
	err := fs.CloneFile(src, target, int64(node.Size))
	if err != nil {
		var cerr *fs.CloneError
		emptied = errors.As(err, &cerr) && cerr.Touched
		debug.Log("%v not cloned from %v (emptied: %v): %v", target, src, emptied, err)
		return false, emptied
	}
	res.clonedFiles++
	res.clonedBytes += node.Size
	return true, false
}

// Cloned returns the number of files the restore cloned (--clone-from) and
// their size.
func (res *Restorer) Cloned() (files uint64, bytes uint64) {
	return res.clonedFiles, res.clonedBytes
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
