//go:build darwin || freebsd || netbsd || linux || solaris

package fs

import (
	"runtime"
	"slices"
	"strings"

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

// xattrKept tells whether the extended attribute name is backed up and
// restored.  BackOtter: not bcachefs_effective.* (Linux), which stock restic
// keeps; rustic-bko leaves them out too.
func xattrKept(name string) bool {
	return runtime.GOOS != "linux" || !strings.HasPrefix(name, bcachefsEffective)
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
	for _, attr := range node.ExtendedAttributes {
		// Only restore xattrs that match the filter
		if xattrSelectFilter(attr.Name) && xattrKept(attr.Name) {
			expectedAttrs[attr.Name] = struct{}{}
			err := setxattr(path, attr.Name, attr.Value)
			switch {
			case xattrUnsupported(err):
				unsupported.add(path, attr.Name, err)
			case err != nil:
				errs = append(errs, err)
			}
		}
	}
	if unsupported.Names != nil {
		errs = append(errs, &unsupported)
	}

	// remove unexpected xattrs
	xattrs, err := listxattr(path)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, name := range xattrs {
		if _, ok := expectedAttrs[name]; ok {
			continue
		}
		// Only attempt to remove xattrs that match the filter
		if xattrSelectFilter(name) && xattrKept(name) {
			if err := removexattr(path, name); err != nil {
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
