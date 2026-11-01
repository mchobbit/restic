package restorer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/ui/progress"
	restoreui "github.com/restic/restic/internal/ui/restore"

	"golang.org/x/sync/errgroup"
)

// Restorer is used to restore a snapshot to a directory.
type Restorer struct {
	repo restic.Repository
	sn   *data.Snapshot
	opts Options

	fileList map[string]bool
	// fileFlags are the files to set file flags and ZFS DOS flags on,
	// last; see RestoreTo.
	fileFlags []fileFlags
	// fileFlagsLinked are the hard-linked files among them, by the inode
	// and device in the snapshot: flags belong to the file, not each name,
	// and an immutable file (on illumos) refuses having them set again.
	fileFlagsLinked map[[2]uint64]bool
	// pendingDirs are the directories, outermost first, above the node
	// the first pass is at that aren't selected themselves but may have
	// selected entries (--include): see ensureParents.
	pendingDirs []pendingDir
	// warned are the warnings given once per restore, by key, with the
	// number of times each came up; see warnAbout.
	warnedMu sync.Mutex
	warned   map[string]*onceWarning
	warnKeys []string

	Error func(location string, err error) error
	Warn  func(message string)
	Info  func(message string)
	// SelectFilter determines whether the item is selectedForRestore or whether a childMayBeSelected.
	// selectedForRestore must not depend on isDir as `removeUnexpectedFiles` always passes false to isDir.
	SelectFilter func(item string, isDir bool) (selectedForRestore bool, childMayBeSelected bool)

	XattrSelectFilter func(xattrName string) (xattrSelectedForRestore bool)

	// TargetNode, when restoring a subfolder of a snapshot ("ID:subfolder"),
	// is the subfolder's own node, whose metadata the target directory gets.
	// Without it the target keeps its own owner, mode, timestamps and
	// extended attributes: harmless for a scratch directory, but when the
	// target is the root of a new file system, that root keeps mkfs's
	// defaults and no SELinux label.
	TargetNode *data.Node
}

var restorerAbortOnAllErrors = func(_ string, err error) error { return err }

type Options struct {
	DryRun          bool
	Sparse          bool
	Progress        *restoreui.Progress
	Overwrite       OverwriteBehavior
	Delete          bool
	OwnershipByName bool
}

type OverwriteBehavior int

// Constants for different overwrite behavior
const (
	OverwriteAlways OverwriteBehavior = iota
	// OverwriteIfChanged is like OverwriteAlways except that it skips restoring the content
	// of files with matching size&mtime. Metadata is always restored.
	OverwriteIfChanged
	OverwriteIfNewer
	OverwriteNever
	OverwriteInvalid
)

// Set implements the method needed for pflag command flag parsing.
func (c *OverwriteBehavior) Set(s string) error {
	switch s {
	case "always":
		*c = OverwriteAlways
	case "if-changed":
		*c = OverwriteIfChanged
	case "if-newer":
		*c = OverwriteIfNewer
	case "never":
		*c = OverwriteNever
	default:
		*c = OverwriteInvalid
		return fmt.Errorf("invalid overwrite behavior %q, must be one of (always|if-changed|if-newer|never)", s)
	}

	return nil
}

func (c *OverwriteBehavior) String() string {
	switch *c {
	case OverwriteAlways:
		return "always"
	case OverwriteIfChanged:
		return "if-changed"
	case OverwriteIfNewer:
		return "if-newer"
	case OverwriteNever:
		return "never"
	default:
		return "invalid"
	}

}
func (c *OverwriteBehavior) Type() string {
	return "behavior"
}

// NewRestorer creates a restorer preloaded with the content from the snapshot id.
func NewRestorer(repo restic.Repository, sn *data.Snapshot, opts Options) *Restorer {
	r := &Restorer{
		repo:              repo,
		opts:              opts,
		fileList:          make(map[string]bool),
		Error:             restorerAbortOnAllErrors,
		SelectFilter:      func(string, bool) (bool, bool) { return true, true },
		XattrSelectFilter: func(string) bool { return true },
		sn:                sn,
	}

	return r
}

type treeVisitor struct {
	enterDir  func(node *data.Node, target, location string) error
	visitNode func(node *data.Node, target, location string) error
	// 'entries' contains all files the snapshot contains for this node. This also includes files
	// ignored by the SelectFilter.
	leaveDir func(node *data.Node, target, location string, entries []string) error
}

func (res *Restorer) sanitizeError(location string, err error) error {
	switch err {
	case nil, context.Canceled, context.DeadlineExceeded:
		// Context errors are permanent.
		return err
	default:
		return res.Error(location, err)
	}
}

// traverseTree traverses a tree from the repo and calls treeVisitor.
// target is the path in the file system, location within the snapshot.
func (res *Restorer) traverseTree(ctx context.Context, target string, treeID restic.ID, visitor treeVisitor) error {
	location := string(filepath.Separator)

	if visitor.enterDir != nil {
		err := res.sanitizeError(location, visitor.enterDir(nil, target, location))
		if err != nil {
			return err
		}
	}
	childFilenames, hasRestored, err := res.traverseTreeInner(ctx, target, location, treeID, visitor)
	if err != nil {
		return err
	}
	if hasRestored && visitor.leaveDir != nil {
		err = res.sanitizeError(location, visitor.leaveDir(nil, target, location, childFilenames))
	}

	return err
}

func (res *Restorer) traverseTreeInner(ctx context.Context, target, location string, treeID restic.ID, visitor treeVisitor) (filenames []string, hasRestored bool, err error) {
	debug.Log("%v %v %v", target, location, treeID)
	tree, err := data.LoadTree(ctx, res.repo, treeID)
	if err != nil {
		debug.Log("error loading tree %v: %v", treeID, err)
		return nil, hasRestored, res.sanitizeError(location, err)
	}

	if res.opts.Delete {
		filenames = make([]string, 0)
	}
	for item := range tree {
		if item.Error != nil {
			debug.Log("error iterating tree %v: %v", treeID, item.Error)
			return nil, hasRestored, res.sanitizeError(location, item.Error)
		}
		node := item.Node
		if ctx.Err() != nil {
			return nil, hasRestored, ctx.Err()
		}

		if res.opts.Delete {
			// just track all files included in the tree node to simplify the control flow.
			// tracking too many files does not matter except for a slightly elevated memory usage
			filenames = append(filenames, node.Name)
		}

		// ensure that the node name does not contain anything that refers to a
		// top-level directory.
		nodeName := filepath.Base(filepath.Join(string(filepath.Separator), node.Name))
		if nodeName != node.Name {
			debug.Log("node %q has invalid name %q", node.Name, nodeName)
			err := res.sanitizeError(location, errors.Errorf("invalid child node name %s", node.Name))
			if err != nil {
				return nil, hasRestored, err
			}
			// force disable deletion to prevent unexpected behavior
			res.opts.Delete = false
			continue
		}

		nodeTarget := filepath.Join(target, nodeName)
		nodeLocation := filepath.Join(location, nodeName)

		if target == nodeTarget || !fs.HasPathPrefix(target, nodeTarget) {
			debug.Log("target: %v %v", target, nodeTarget)
			debug.Log("node %q has invalid target path %q", node.Name, nodeTarget)
			err := res.sanitizeError(nodeLocation, errors.New("node has invalid path"))
			if err != nil {
				return nil, hasRestored, err
			}
			// force disable deletion to prevent unexpected behavior
			res.opts.Delete = false
			continue
		}

		// sockets cannot be restored
		if node.Type == data.NodeTypeSocket {
			continue
		}

		selectedForRestore, childMayBeSelected := res.SelectFilter(nodeLocation, node.Type == data.NodeTypeDir)
		debug.Log("SelectFilter returned %v %v for %q", selectedForRestore, childMayBeSelected, nodeLocation)

		if selectedForRestore {
			hasRestored = true
		}

		if node.Type == data.NodeTypeDir {
			if node.Subtree == nil {
				return nil, hasRestored, errors.Errorf("Dir without subtree in tree %v", treeID.Str())
			}

			if selectedForRestore && visitor.enterDir != nil {
				err = res.sanitizeError(nodeLocation, visitor.enterDir(node, nodeTarget, nodeLocation))
				if err != nil {
					return nil, hasRestored, err
				}
			}

			// keep track of restored child status
			// so metadata of the current directory are restored on leaveDir
			childHasRestored := false
			var childFilenames []string

			if childMayBeSelected {
				pending := !selectedForRestore
				if pending {
					res.pendingDirs = append(res.pendingDirs, pendingDir{target: nodeTarget, location: nodeLocation, node: node})
				}
				childFilenames, childHasRestored, err = res.traverseTreeInner(ctx, nodeTarget, nodeLocation, *node.Subtree, visitor)
				if pending {
					res.pendingDirs = res.pendingDirs[:len(res.pendingDirs)-1]
				}
				err = res.sanitizeError(nodeLocation, err)
				if err != nil {
					return nil, hasRestored, err
				}
				// inform the parent directory to restore parent metadata on leaveDir if needed
				if childHasRestored {
					hasRestored = true
				}
			}

			// metadata need to be restore when leaving the directory in both cases
			// selected for restore or any child of any subtree have been restored
			if (selectedForRestore || childHasRestored) && visitor.leaveDir != nil {
				err = res.sanitizeError(nodeLocation, visitor.leaveDir(node, nodeTarget, nodeLocation, childFilenames))
				if err != nil {
					return nil, hasRestored, err
				}
			}

			continue
		}

		if selectedForRestore {
			err = res.sanitizeError(nodeLocation, visitor.visitNode(node, nodeTarget, nodeLocation))
			if err != nil {
				return nil, hasRestored, err
			}
		}
	}

	return filenames, hasRestored, nil
}

func (res *Restorer) restoreNodeTo(node *data.Node, target, location string) error {
	if !res.opts.DryRun {
		debug.Log("restoreNode %v %v %v", node.Name, target, location)
		// BackOtter: flags that refuse removal come off first.
		fs.ClearRestrictingFlags(target)
		if err := fs.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "RemoveNode")
		}

		err := fs.NodeCreateAt(node, target)
		if err != nil {
			debug.Log("node.CreateAt(%s) error %v", target, err)
			return err
		}
	}

	res.opts.Progress.AddProgress(location, restoreui.ActionOtherRestored, 0, 0)
	return res.restoreNodeMetadataTo(node, target, location)
}

func (res *Restorer) restoreNodeMetadataTo(node *data.Node, target, location string) error {
	if res.opts.DryRun {
		return nil
	}
	debug.Log("restoreNodeMetadata %v %v %v", node.Name, target, location)
	err := fs.NodeRestoreMetadata(node, target, res.Warn, res.XattrSelectFilter, res.opts.OwnershipByName)
	if err != nil {
		debug.Log("node.RestoreMetadata(%s) error %v", target, err)
		err = res.warnAboutAll(location, err)
	}
	flags, hasFlags := res.nodeFileFlags(node, location)
	dosFlags, hasDosFlags := res.nodeZFSDosFlags(node, location)
	if (hasFlags || hasDosFlags) && !res.flagsQueued(node) {
		res.fileFlags = append(res.fileFlags, fileFlags{
			target: target, location: location, typ: node.Type,
			flags: flags, hasFlags: hasFlags,
			dosFlags: dosFlags, hasDosFlags: hasDosFlags,
			compression: res.btrfsCompression(node),
		})
	}
	return err
}

// nodeFileFlags returns the file flags node holds for this OS, and whether
// it holds any, warning once about malformed ones (which are none).
func (res *Restorer) nodeFileFlags(node *data.Node, location string) (uint32, bool) {
	flags, ok, err := fs.NodeFileFlags(node)
	if err != nil {
		res.warnAbout(location, err)
	}
	return flags, ok
}

// nodeZFSDosFlags returns the ZFS DOS flags node holds for this OS, and
// whether it holds them, warning once about malformed ones (which are none).
func (res *Restorer) nodeZFSDosFlags(node *data.Node, location string) (uint64, bool) {
	flags, ok, err := fs.NodeZFSDosFlags(node)
	if err != nil {
		res.warnAbout(location, err)
	}
	return flags, ok
}

// nodeSparse tells whether node is a file that had holes when it was backed
// up, warning once about a malformed marker (which is none).
func (res *Restorer) nodeSparse(node *data.Node, location string) bool {
	sparse, err := node.Sparse()
	if err != nil {
		res.warnAbout(location, err)
	}
	return sparse
}

// btrfsCompression returns node's btrfs.compression extended attribute,
// when it has one and the restore sets it, for RestoreFileFlags to set again
// after the flags; nil otherwise.
func (res *Restorer) btrfsCompression(node *data.Node) []byte {
	const name = "btrfs.compression"
	if !res.XattrSelectFilter(name) {
		return nil
	}
	for _, attr := range node.ExtendedAttributes {
		if attr.Name == name {
			return append([]byte{}, attr.Value...)
		}
	}
	return nil
}

// warnUnread warns that node, a regular file restored at location, is
// restored empty when the backup couldn't read its contents (av_quarantined
// kept the file closed), with their size then, and tells whether it is.
// Not an error: the file comes back as it was otherwise, its system
// attributes, av_quarantined among them, last as always.
func (res *Restorer) warnUnread(node *data.Node, location string) bool {
	size, ok, err := node.UnreadSize()
	if err != nil {
		res.warnAbout(location, err)
		return false
	}
	if ok {
		res.warn(fmt.Sprintf("%v: contents were not backed up (av_quarantined at backup, %d bytes)", location, size))
	}
	return ok
}

// onceWarning is a warning given once per restore: its text without the
// path, the location it was given for, and how often it came up (when that
// is counted).
type onceWarning struct {
	text, first string
	count       int
}

// warnAboutAll gives the warnings in err (fs.IsWarning) and returns the rest.
func (res *Restorer) warnAboutAll(location string, err error) error {
	rest, warnings := fs.SplitWarnings(err)
	for _, w := range warnings {
		res.warnAbout(location, w)
	}
	return rest
}

// warnAbout gives the warning err for location.  Metadata the target file
// system doesn't support (fs.UnsupportedError) is a warning once per
// attribute and bits, with the number of files at the end (warnTotals), and
// a malformed attribute (data.MalformedAttributeError) once per attribute,
// as is a SID idmap couldn't map (fs.SIDWarning), with the number of files.
// None is an error: the restore succeeds.  Other warnings (fs.FlagsWarning)
// are given every time.
func (res *Restorer) warnAbout(location string, err error) {
	var unsupported *fs.UnsupportedError
	var sid *fs.SIDWarning
	var malformed *data.MalformedAttributeError
	var key, text, msg string
	counted := false
	switch {
	case errors.As(err, &unsupported):
		text = fmt.Sprintf("%v", unsupported.Attr)
		if unsupported.Bits != 0 {
			text = fmt.Sprintf("%v %#x", unsupported.Attr, unsupported.Bits)
		}
		text += " not supported by the target file system"
		key, counted = text, true
		msg = fmt.Sprintf("%v: %v: %v", location, text, unsupported.Err)
	case errors.As(err, &sid):
		text = fmt.Sprintf("%v: SIDs not mapped by idmap, nobody (60001) used", sid.Attr)
		key, counted = text, true
		msg = fmt.Sprintf("%v: %v: %v: %v", location, text, sid.SID, sid.Err)
	case errors.As(err, &malformed):
		text = fmt.Sprintf("ignoring malformed %v", malformed.Attr)
		key = text
		msg = fmt.Sprintf("ignoring %v of %v: %v", malformed.Attr, location, malformed.Err)
	default:
		res.warn(err.Error())
		return
	}

	res.warnedMu.Lock()
	w, ok := res.warned[key]
	if !ok {
		if res.warned == nil {
			res.warned = make(map[string]*onceWarning)
		}
		w = &onceWarning{text: text, first: location}
		res.warned[key] = w
		res.warnKeys = append(res.warnKeys, key)
	}
	if counted {
		w.count++
	}
	res.warnedMu.Unlock()
	if !ok {
		res.warn(msg)
	}
}

// warnTotals gives the number of files each warning given once came up for,
// where that was more than one.
func (res *Restorer) warnTotals() {
	res.warnedMu.Lock()
	defer res.warnedMu.Unlock()
	for _, key := range res.warnKeys {
		if w := res.warned[key]; w.count > 1 {
			res.warn(fmt.Sprintf("%v: %d files in all, the first %v", w.text, w.count, w.first))
		}
	}
	res.warned, res.warnKeys = nil, nil
}

func (res *Restorer) warn(msg string) {
	if res.Warn == nil {
		debug.Log("warning: %v", msg)
		return
	}
	res.Warn(msg)
}

// flagsQueued tells whether node is another name of a hard-linked file whose
// flags are already queued, and notes it if not.
func (res *Restorer) flagsQueued(node *data.Node) bool {
	if node.Type != data.NodeTypeFile || node.Links <= 1 {
		return false
	}
	key := [2]uint64{node.Inode, node.DeviceID}
	if res.fileFlagsLinked[key] {
		return true
	}
	if res.fileFlagsLinked == nil {
		res.fileFlagsLinked = make(map[[2]uint64]bool)
	}
	res.fileFlagsLinked[key] = true
	return false
}

// fileFlags is a file whose file flags, ZFS DOS flags or both the restorer
// sets once it has finished with every file.
type fileFlags struct {
	target, location string
	typ              data.NodeType
	flags            uint32
	hasFlags         bool
	dosFlags         uint64
	hasDosFlags      bool
	compression      []byte // btrfs.compression, to set again after the flags
}

// restoreFileFlags sets the file flags noted while restoring metadata.  They
// come last: an immutable file refuses new hard links, timestamps and
// permissions, an immutable directory new entries, and an append-only file
// its contents.  ZFS DOS flags go on just before: a readonly file refuses
// writes, a nounlink one renames, any write sets archive again, and setting
// them needs no privilege while immutable and append-only don't change.
func (res *Restorer) restoreFileFlags() error {
	for _, f := range res.fileFlags {
		if f.hasDosFlags {
			debug.Log("restoreZFSDosFlags %v %#x", f.target, f.dosFlags)
			if err := fs.RestoreZFSDosFlags(f.target, f.typ, f.dosFlags); err != nil {
				if err := res.sanitizeError(f.location, res.warnAboutAll(f.location, err)); err != nil {
					return err
				}
			}
		}
		if !f.hasFlags {
			continue
		}
		debug.Log("restoreFileFlags %v %#x", f.target, f.flags)
		if err := fs.RestoreFileFlags(f.target, f.typ, f.flags, f.compression); err != nil {
			if err := res.sanitizeError(f.location, res.warnAboutAll(f.location, err)); err != nil {
				return err
			}
		}
	}
	res.fileFlags, res.fileFlagsLinked = nil, nil
	return nil
}

func (res *Restorer) restoreHardlinkAt(node *data.Node, target, path, location string) error {
	if !res.opts.DryRun {
		fs.ClearRestrictingFlags(path)
		if err := fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "RemoveCreateHardlink")
		}
		err := fs.Link(target, path)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	res.opts.Progress.AddProgress(location, restoreui.ActionOtherRestored, 0, 0)
	// TODO investigate if hardlinks have separate metadata on any supported system
	return res.restoreNodeMetadataTo(node, path, location)
}

func (res *Restorer) ensureDir(target string) error {
	if res.opts.DryRun {
		return nil
	}

	fi, err := fs.Lstat(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to check for directory: %w", err)
	}
	if err == nil && !fi.IsDir() {
		// try to cleanup unexpected file
		fs.ClearRestrictingFlags(target)
		if err := fs.Remove(target); err != nil {
			return fmt.Errorf("failed to remove stale item: %w", err)
		}
	}

	// create parent dir with default permissions
	// second pass #leaveDir restores dir metadata after visiting/restoring all children
	return fs.MkdirAll(target, 0700)
}

// prepareDirForFlags is fs.PrepareDirForFlags, replaced by tests.
var prepareDirForFlags = fs.PrepareDirForFlags

// ensureDirWithFlags is ensureDir for a directory whose node has the file
// flags flags (0 for none).  When it creates the directory, or finds it
// empty, it makes those flags that a file system only changes on an empty
// directory (casefolding) what flags says at once, before anything goes in:
// it sets them, or clears them where the directory inherited them.  The
// others are set last, with restoreFileFlags.
func (res *Restorer) ensureDirWithFlags(target string, flags uint32) error {
	if res.opts.DryRun {
		return nil
	}
	// BackOtter: what the restore finds in its way loses the flags that
	// would refuse its getting entries, metadata or being replaced
	// (fs.ClearRestrictingFlags); the late flags pass sets the node's.
	fs.ClearRestrictingFlags(target)
	if !fs.HasDirFlagsBeforeEntries() {
		return res.ensureDir(target)
	}
	fi, err := fs.Lstat(target)
	created := errors.Is(err, os.ErrNotExist) || err == nil && !fi.IsDir()
	if err := res.ensureDir(target); err != nil {
		return err
	}
	if !created {
		empty, err := fs.IsEmptyDir(target)
		if err != nil {
			debug.Log("not preparing %v for its flags: %v", target, err)
			return nil
		}
		if !empty {
			return nil
		}
	}
	return prepareDirForFlags(target, flags)
}

// pendingDir is a directory that isn't selected for the restore, but may
// have selected entries (--include).
type pendingDir struct {
	target, location string
	node             *data.Node
	done             bool
}

// ensureParents creates the pending directories above the node the first
// pass is at, outermost first, each with its flags (ensureDirWithFlags): an
// --include that selects only some of a directory's entries leaves it
// unselected, but it is still created, and gets its metadata at the end.
func (res *Restorer) ensureParents() error {
	for i := range res.pendingDirs {
		p := &res.pendingDirs[i]
		if p.done {
			continue
		}
		flags, _ := res.nodeFileFlags(p.node, p.location)
		if err := res.ensureDirWithFlags(p.target, flags); err != nil {
			return err
		}
		p.done = true
	}
	return nil
}

// RestoreTo creates the directories and files in the snapshot below dst.
// Before an item is created, res.Filter is called.
func (res *Restorer) RestoreTo(ctx context.Context, dst string) (uint64, error) {
	restoredFileCount := uint64(0)
	var err error
	if !filepath.IsAbs(dst) {
		dst, err = filepath.Abs(dst)
		if err != nil {
			return restoredFileCount, errors.Wrap(err, "Abs")
		}
	}

	if !res.opts.DryRun {
		// ensure that the target directory exists and is actually a directory
		// Using ensureDir is too aggressive here as it also removes unexpected files
		if err := fs.MkdirAll(dst, 0700); err != nil {
			return restoredFileCount, fmt.Errorf("cannot create target directory: %w", err)
		}
	}

	idx := NewHardlinkIndex[string]()
	filerestorer := newFileRestorer(dst, res.repo.LoadBlobsFromPack, res.repo.LookupBlob,
		res.repo.Connections(), res.opts.Sparse, res.opts.Delete, res.repo.StartWarmup, res.opts.Progress)
	filerestorer.Error = res.Error
	filerestorer.Info = res.Info

	debug.Log("first pass for %q", dst)

	var buf []byte

	// first tree pass: create directories and collect all files to restore
	err = res.traverseTree(ctx, dst, *res.sn.Tree, treeVisitor{
		enterDir: func(node *data.Node, target, location string) error {
			debug.Log("first pass, enterDir: mkdir %q, leaveDir should restore metadata", location)
			if location != string(filepath.Separator) {
				res.opts.Progress.AddFile(0)
			}
			if node == nil {
				// The target itself: when restoring "ID:subfolder", it
				// has the subfolder's node, and gets its casefolding
				// while empty, as any other directory.
				if res.TargetNode == nil {
					return res.ensureDir(target)
				}
				node = res.TargetNode
			} else if err := res.ensureParents(); err != nil {
				return err
			}
			flags, _ := res.nodeFileFlags(node, location)
			return res.ensureDirWithFlags(target, flags)
		},

		visitNode: func(node *data.Node, target, location string) error {
			debug.Log("first pass, visitNode: mkdir %q, leaveDir on second pass should restore metadata", location)
			if err := res.ensureParents(); err != nil {
				return err
			}
			if err := res.ensureDir(filepath.Dir(target)); err != nil {
				return err
			}

			if node.Type != data.NodeTypeFile {
				res.opts.Progress.AddFile(0)
				return nil
			}

			if node.Links > 1 {
				if idx.Has(node.Inode, node.DeviceID) {
					// a hardlinked file does not increase the restore size
					res.opts.Progress.AddFile(0)
					return nil
				}
				idx.Add(node.Inode, node.DeviceID, location)
			}

			sparse := res.nodeSparse(node, location)
			buf, err = res.withOverwriteCheck(ctx, node, target, location, false, buf, func(updateMetadataOnly bool, matches *fileState) error {
				// BackOtter: a file in the way loses the flags that
				// would refuse its being rewritten, replaced or given
				// its metadata (immutable, append-only, undeletable),
				// as in rustic-bko; the late flags pass sets the
				// node's.
				if !res.opts.DryRun {
					fs.ClearRestrictingFlags(target)
				}
				unread := res.warnUnread(node, location)
				if updateMetadataOnly {
					res.opts.Progress.AddSkippedFile(location, node.Size)
					// BackOtter: a file whose contents are right, but
					// that should be fully allocated, gets its holes
					// filled (a swap file from an interrupted restore).
					if !res.opts.DryRun && !res.opts.Sparse && !sparse {
						fs.FillHoles(target, int64(node.Size))
					}
				} else {
					res.opts.Progress.AddFile(node.Size)
					if !res.opts.DryRun && unread {
						// A file whose contents weren't backed up
						// goes into a new, empty one: the one there
						// may have av_quarantined, which keeps it
						// closed to the restore too.
						if err := fs.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
							if err := res.sanitizeError(location, err); err != nil {
								return err
							}
						}
					}
					if !res.opts.DryRun {
						// Whether the file goes into a new, empty file,
						// which alone may be written sparse: a hole in
						// an existing one would keep its old bytes.  The
						// restorer replaces anything but a regular file.
						fi, err := fs.Lstat(target)
						fresh := errors.Is(err, os.ErrNotExist) || err == nil && !fi.Mode().IsRegular()
						// Flags a file system only takes on an empty
						// file (btrfs's no-copy-on-write) go on now,
						// before the file restorer writes its data.
						if flags, ok := res.nodeFileFlags(node, location); ok {
							if err := fs.PrepareFileForFlags(target, flags); err != nil {
								if err := res.sanitizeError(location, err); err != nil {
									return err
								}
							}
						}
						filerestorer.addFile(location, node.Content, int64(node.Size), matches, sparse, fresh)
					} else {
						action := restoreui.ActionFileUpdated
						if matches == nil {
							action = restoreui.ActionFileRestored
						}
						// immediately mark as completed
						res.opts.Progress.AddProgress(location, action, node.Size, node.Size)
					}
				}
				res.trackFile(location, updateMetadataOnly)
				if !updateMetadataOnly {
					restoredFileCount++
				}
				return nil
			})
			return err
		},
	})
	if err != nil {
		return 0, err
	}

	if !res.opts.DryRun {
		err = filerestorer.restoreFiles(ctx)
		if err != nil {
			return 0, err
		}
	}

	debug.Log("second pass for %q", dst)

	// second tree pass: restore special files and filesystem metadata
	err = res.traverseTree(ctx, dst, *res.sn.Tree, treeVisitor{
		visitNode: func(node *data.Node, target, location string) error {
			debug.Log("second pass, visitNode: restore node %q", location)
			if node.Type != data.NodeTypeFile {
				_, err := res.withOverwriteCheck(ctx, node, target, location, false, nil, func(_ bool, _ *fileState) error {
					return res.restoreNodeTo(node, target, location)
				})
				return err
			}

			if idx.Has(node.Inode, node.DeviceID) && idx.Value(node.Inode, node.DeviceID) != location {
				_, err := res.withOverwriteCheck(ctx, node, target, location, true, nil, func(_ bool, _ *fileState) error {
					return res.restoreHardlinkAt(node, filerestorer.targetPath(idx.Value(node.Inode, node.DeviceID)), target, location)
				})
				return err
			}

			if _, ok := res.hasRestoredFile(location); ok {
				return res.restoreNodeMetadataTo(node, target, location)
			}
			// don't touch skipped files
			return nil
		},
		leaveDir: func(node *data.Node, target, location string, expectedFilenames []string) error {
			if res.opts.Delete {
				if err := res.removeUnexpectedFiles(ctx, target, location, expectedFilenames); err != nil {
					return err
				}
			}

			if node == nil {
				// The target itself: it gets the subfolder's metadata
				// when restoring "ID:subfolder", and is left as it is
				// for a whole snapshot, which has no node of its own.
				if res.TargetNode == nil {
					return nil
				}
				return res.restoreNodeMetadataTo(res.TargetNode, target, location)
			}

			err := res.restoreNodeMetadataTo(node, target, location)
			if err == nil {
				res.opts.Progress.AddProgress(location, restoreui.ActionDirRestored, 0, 0)
			}
			return err
		},
	})
	if err != nil {
		return restoredFileCount, err
	}
	err = res.restoreFileFlags()
	res.warnTotals()
	return restoredFileCount, err
}

func (res *Restorer) removeUnexpectedFiles(ctx context.Context, target, location string, expectedFilenames []string) error {
	if !res.opts.Delete {
		panic("internal error")
	}

	entries, err := fs.Readdirnames(fs.Local{}, target, fs.O_NOFOLLOW)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	keep := map[string]struct{}{}
	for _, name := range expectedFilenames {
		keep[toComparableFilename(name)] = struct{}{}
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if _, ok := keep[toComparableFilename(entry)]; ok {
			continue
		}

		nodeTarget := filepath.Join(target, entry)
		nodeLocation := filepath.Join(location, entry)

		if target == nodeTarget || !fs.HasPathPrefix(target, nodeTarget) {
			return fmt.Errorf("skipping deletion due to invalid filename: %v", entry)
		}

		// TODO pass a proper value to the isDir parameter once this becomes relevant for the filters
		selectedForRestore, _ := res.SelectFilter(nodeLocation, false)
		// only delete files that were selected for restore
		if selectedForRestore {
			// First collect all files that will be deleted
			var filesToDelete []string
			err := filepath.Walk(nodeTarget, func(path string, _ os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				filesToDelete = append(filesToDelete, path)
				return nil
			})
			if err != nil {
				return err
			}

			if !res.opts.DryRun {
				// BackOtter: flags that refuse removal come off
				// first, outermost first.
				for _, path := range filesToDelete {
					fs.ClearRestrictingFlags(path)
				}
				// Perform the deletion
				if err := fs.RemoveAll(nodeTarget); err != nil {
					return err
				}
			}

			// Report paths as deleted only after successful removal
			for i := len(filesToDelete) - 1; i >= 0; i-- {
				res.opts.Progress.ReportDeletion(filesToDelete[i])
			}
		}
	}

	return nil
}

func (res *Restorer) trackFile(location string, metadataOnly bool) {
	res.fileList[location] = metadataOnly
}

func (res *Restorer) hasRestoredFile(location string) (metadataOnly bool, ok bool) {
	metadataOnly, ok = res.fileList[location]
	return metadataOnly, ok
}

func (res *Restorer) withOverwriteCheck(ctx context.Context, node *data.Node, target, location string, isHardlink bool, buf []byte, cb func(updateMetadataOnly bool, matches *fileState) error) ([]byte, error) {
	overwrite, err := shouldOverwrite(res.opts.Overwrite, node, target)
	if err != nil {
		return buf, err
	} else if !overwrite {
		size := node.Size
		if isHardlink {
			size = 0
		}
		res.opts.Progress.AddSkippedFile(location, size)
		return buf, nil
	}

	var matches *fileState
	updateMetadataOnly := false
	if node.Type == data.NodeTypeFile && !isHardlink {
		// if a file fails to verify, then matches is nil which results in restoring from scratch
		matches, buf, _ = res.verifyFile(ctx, target, node, false, res.opts.Overwrite == OverwriteIfChanged, buf)
		// skip files that are already correct completely
		updateMetadataOnly = !matches.NeedsRestore()
	}

	return buf, cb(updateMetadataOnly, matches)
}

func shouldOverwrite(overwrite OverwriteBehavior, node *data.Node, destination string) (bool, error) {
	if overwrite == OverwriteAlways || overwrite == OverwriteIfChanged {
		return true, nil
	}

	fi, err := fs.Lstat(destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	switch overwrite {
	case OverwriteIfNewer:
		// return if node is newer
		return node.ModTime.After(fi.ModTime()), nil
	case OverwriteNever:
		// file exists
		return false, nil
	}
	panic("unknown overwrite behavior")
}

// Snapshot returns the snapshot this restorer is configured to use.
func (res *Restorer) Snapshot() *data.Snapshot {
	return res.sn
}

// Number of workers in VerifyFiles.
const nVerifyWorkers = 8

// VerifyFiles checks whether all regular files in the snapshot res.sn
// have been successfully written to dst. It stops when it encounters an
// error. It returns that error and the number of files it has successfully
// verified.
func (res *Restorer) VerifyFiles(ctx context.Context, dst string, countRestoredFiles uint64, p *progress.Counter) (int, error) {
	type mustCheck struct {
		node *data.Node
		path string
	}

	var (
		nchecked uint64
		work     = make(chan mustCheck, 2*nVerifyWorkers)
	)

	p.SetMax(countRestoredFiles)
	defer p.Done()

	g, ctx := errgroup.WithContext(ctx)

	// Traverse tree and send jobs to work.
	g.Go(func() error {
		defer close(work)

		err := res.traverseTree(ctx, dst, *res.sn.Tree, treeVisitor{
			visitNode: func(node *data.Node, target, location string) error {
				if node.Type != data.NodeTypeFile {
					return nil
				}
				if metadataOnly, ok := res.hasRestoredFile(location); !ok || metadataOnly {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case work <- mustCheck{node, target}:
					return nil
				}
			},
		})
		return err
	})

	for i := 0; i < nVerifyWorkers; i++ {
		g.Go(func() (err error) {
			var buf []byte
			for job := range work {
				_, buf, err = res.verifyFile(ctx, job.path, job.node, true, false, buf)
				err = res.sanitizeError(job.path, err)
				if err != nil || ctx.Err() != nil {
					break
				}
				p.Add(1)
				atomic.AddUint64(&nchecked, 1)
			}
			return err
		})
	}

	return int(nchecked), g.Wait()
}

type fileState struct {
	blobMatches []bool
	sizeMatches bool
}

func (s *fileState) NeedsRestore() bool {
	if s == nil {
		return true
	}
	if !s.sizeMatches {
		return true
	}
	for _, match := range s.blobMatches {
		if !match {
			return true
		}
	}
	return false
}

func (s *fileState) HasMatchingBlob(i int) bool {
	if s == nil || s.blobMatches == nil {
		return false
	}
	return i < len(s.blobMatches) && s.blobMatches[i]
}

// Verify that the file target has the contents of node.
//
// buf and the first return value are scratch space, passed around for reuse.
// Reusing buffers prevents the verifier goroutines allocating all of RAM and
// flushing the filesystem cache (at least on Linux).
func (res *Restorer) verifyFile(ctx context.Context, target string, node *data.Node, failFast bool, trustMtime bool, buf []byte) (*fileState, []byte, error) {
	f, err := fs.OpenFile(target, fs.O_RDONLY|fs.O_NOFOLLOW, 0)
	if err != nil {
		return nil, buf, err
	}
	// BackOtter: reading the file leaves its atime, restored or not, alone.
	fs.SetNoatime(f)
	defer func() {
		_ = f.Close()
	}()

	fi, err := f.Stat()
	sizeMatches := true
	switch {
	case err != nil:
		return nil, buf, err
	case !fi.Mode().IsRegular():
		return nil, buf, errors.Errorf("Expected %s to be a regular file", target)
	case int64(node.Size) != fi.Size():
		if failFast {
			return nil, buf, errors.Errorf("Invalid file size for %s: expected %d, got %d",
				target, node.Size, fi.Size())
		}
		sizeMatches = false
	}

	if trustMtime && fi.ModTime().Equal(node.ModTime) && sizeMatches {
		return &fileState{nil, sizeMatches}, buf, nil
	}

	matches := make([]bool, len(node.Content))
	var offset int64
	for i, blobID := range node.Content {
		if ctx.Err() != nil {
			return nil, buf, ctx.Err()
		}
		length, found := res.repo.LookupBlobSize(restic.DataBlob, blobID)
		if !found {
			return nil, buf, errors.Errorf("Unable to fetch blob %s", blobID)
		}

		if length > uint(cap(buf)) {
			buf = make([]byte, 2*length)
		}
		buf = buf[:length]

		_, err = f.ReadAt(buf, offset)
		if err == io.EOF && !failFast {
			sizeMatches = false
			break
		}
		if err != nil {
			return nil, buf, err
		}
		matches[i] = blobID.Equal(restic.Hash(buf))
		if failFast && !matches[i] {
			return nil, buf, errors.Errorf(
				"Unexpected content in %s, starting at offset %d",
				target, offset)
		}
		offset += int64(length)
	}

	return &fileState{matches, sizeMatches}, buf, nil
}
