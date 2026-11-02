//go:build darwin || freebsd || netbsd || linux || solaris

package fs

import (
	"bytes"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// getxattr, listxattr, setxattr and removexattr are in xattr_pkg.go (pkg/xattr),
// and in xattr_illumos.go on illumos.

// bcachefsEffective is the prefix of bcachefs' extended attributes that show
// the options a file has, its own (bcachefs.*, kept) or inherited from its
// directory, and its project ID: derived, and setting one does nothing.
const bcachefsEffective = "bcachefs_effective."

// bcachefsOwnProject is the extended attribute in which bcachefs shows a
// project ID a file was given, not inherited (bcachefs 1.39 lists it; Linux
// 6.12's doesn't).  The project ID is kept as linux.project_id; bcachefs refuses to
// have the attribute set to anything but 0 (EINVAL).
const bcachefsOwnProject = "bcachefs.project"

// xattrKept tells whether the extended attribute name is backed up and
// restored.  BackOtter: not bcachefs_effective.* (Linux), which stock restic
// keeps, nor bcachefs.project, which linux.project_id carries; rustic-bko
// leaves them out too.  A restore neither sets nor removes them, whatever a
// snapshot holds.
func xattrKept(name string) bool {
	return runtime.GOOS != "linux" ||
		!strings.HasPrefix(name, bcachefsEffective) && name != bcachefsOwnProject
}

// bcachefsCasefold is the extended attribute that holds a bcachefs
// directory's casefolding option, which bcachefs sets, clears and even sets
// again to the value it has only while the directory is empty (ENOTEMPTY).
// A restore gives it to each directory it creates, or finds empty, before
// anything goes in (PrepareDirForOptions).
const bcachefsCasefold = "bcachefs.casefold"

// casefoldNotEmpty tells whether err, from setting or removing the extended
// attribute name, is bcachefs refusing to change the casefolding of a
// directory with entries: a warning, as for chattr's F (S6), not an error.
func casefoldNotEmpty(name string, err error) bool {
	return name == bcachefsCasefold && errors.Is(err, syscall.ENOTEMPTY)
}

// casefoldWarning is the warning for casefoldNotEmpty's refusal on path.
func casefoldWarning(path string) error {
	return &FlagsWarning{Msg: fmt.Sprintf("casefolding (%v) not restored on %v: directory not empty", bcachefsCasefold, path)}
}

// setxattrFunc, listxattrFunc and removexattrFunc are setxattr, listxattr
// and removexattr, replaced by tests.
var (
	setxattrFunc    = setxattr
	listxattrFunc   = listxattr
	removexattrFunc = removexattr
)

// selinuxLabel is the extended attribute that holds a file's SELinux label.
const selinuxLabel = "security.selinux"

// xattrNeverRemoved tells whether a restore leaves the extended attribute
// name alone on a file whose node hasn't got it, rather than removing it.
// BackOtter: the SELinux label, which the system gives every new file and
// SELinux never lets anyone remove (EACCES, even as root): a node without
// one (from a system without SELinux, a btrfs subvolume's root, a
// directory above the backed-up path in an old rustic-bko snapshot) keeps
// the label the restore's file got, as in rustic-bko.  A node with one sets
// it.
func xattrNeverRemoved(name string) bool {
	return name == selinuxLabel
}

func nodeRestoreExtendedAttributes(node *data.Node, path string, xattrSelectFilter func(xattrName string) bool) error {
	// BackOtter: an attribute that can't be set (a trusted.* one when not
	// running as root) doesn't keep the others from being set or removed;
	// the errors are all returned, each naming its attribute.  Those the
	// file system doesn't support (none on NetBSD's FFSv1) are one
	// XattrsWarning naming them all, not nothing.
	var errs []error
	var unsupported XattrsWarning
	expectedAttrs := map[string]struct{}{}

	// BackOtter: an attribute the file has already, with its node's value,
	// is left as it is, as in rustic-bko: bcachefs refuses to set
	// bcachefs.casefold again on a directory with entries, even to the
	// value it has, which the directory got as soon as the restore created
	// it (PrepareDirForOptions).  The file's attributes are listed first;
	// those it doesn't list are all set.
	xattrs, listErr := listxattrFunc(path)
	present := make(map[string]struct{}, len(xattrs))
	for _, name := range xattrs {
		present[name] = struct{}{}
	}

	for _, attr := range node.ExtendedAttributes {
		// Only restore xattrs that match the filter
		if xattrSelectFilter(attr.Name) && xattrKept(attr.Name) {
			expectedAttrs[attr.Name] = struct{}{}
			if _, ok := present[attr.Name]; ok {
				if value, err := getxattr(path, attr.Name); err == nil && bytes.Equal(value, attr.Value) {
					continue
				}
			}
			err := setxattrFunc(path, attr.Name, attr.Value)
			switch {
			case xattrUnsupported(err):
				unsupported.add(path, attr.Name, err)
			case casefoldNotEmpty(attr.Name, err):
				errs = append(errs, casefoldWarning(path))
			case err != nil:
				errs = append(errs, err)
			}
		}
	}
	if unsupported.Names != nil {
		errs = append(errs, &unsupported)
	}

	// remove unexpected xattrs
	if listErr != nil {
		return errors.Join(append(errs, listErr)...)
	}
	for _, name := range xattrs {
		if _, ok := expectedAttrs[name]; ok {
			continue
		}
		// Only attempt to remove xattrs that match the filter
		// BackOtter: nor the SELinux label (xattrNeverRemoved).
		if xattrSelectFilter(name) && xattrKept(name) && !xattrNeverRemoved(name) {
			err := removexattrFunc(path, name)
			switch {
			case casefoldNotEmpty(name, err):
				errs = append(errs, casefoldWarning(path))
			case err != nil:
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

func nodeFillExtendedAttributes(node *data.Node, path string, ignoreListError bool, warnf func(format string, args ...any)) error {
	xattrs, err := listxattr(path)
	debug.Log("fillExtendedAttributes(%v) %v %v", path, xattrs, err)
	if err != nil {
		if ignoreListError && isListxattrPermissionError(err) {
			return nil
		}
		return err
	}
	// BackOtter: stored in byte order of their names, as rustic-bko stores
	// them, not in the order the file system lists them; and only those
	// kept.
	xattrs = slices.DeleteFunc(xattrs, func(name string) bool { return !xattrKept(name) })
	slices.Sort(xattrs)

	node.ExtendedAttributes = make([]data.ExtendedAttribute, 0, len(xattrs))
	for _, attr := range xattrs {
		attrVal, err := getxattr(path, attr)
		if err != nil {
			warnf("can not obtain extended attribute %v for %v: %v\n", attr, path, err)
			continue
		}
		attr := data.ExtendedAttribute{
			Name:  attr,
			Value: attrVal,
		}

		node.ExtendedAttributes = append(node.ExtendedAttributes, attr)
	}

	return nil
}
