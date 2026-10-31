//go:build darwin || freebsd || netbsd || linux || solaris

package fs

import (
	"slices"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
)

// getxattr, listxattr, setxattr and removexattr are in xattr_pkg.go (pkg/xattr),
// and in xattr_illumos.go on illumos.

func nodeRestoreExtendedAttributes(node *data.Node, path string, xattrSelectFilter func(xattrName string) bool) error {
	// BackOtter: an attribute that can't be set (a trusted.* one when not
	// running as root) doesn't keep the others from being set or removed;
	// the errors are all returned, each naming its attribute.
	var errs []error
	expectedAttrs := map[string]struct{}{}
	for _, attr := range node.ExtendedAttributes {
		// Only restore xattrs that match the filter
		if xattrSelectFilter(attr.Name) {
			expectedAttrs[attr.Name] = struct{}{}
			if err := setxattr(path, attr.Name, attr.Value); err != nil {
				errs = append(errs, err)
			}
		}
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
		if xattrSelectFilter(name) {
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
	// them, not in the order the file system lists them.
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
