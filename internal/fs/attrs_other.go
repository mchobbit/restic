//go:build !freebsd

package fs

import "github.com/restic/restic/internal/data"

// FreeBSD's system extended attributes and ACLs (attrs_freebsd.go) have no
// counterpart here.

//nolint:unused // not used on Windows
func nodeFillFreeBSDAttributes(_ *data.Node, _ string, _ *ExtendedFileInfo) error {
	return nil
}

//nolint:unused // not used on Windows
func nodeRestoreFreeBSDExtattrs(_ *data.Node, _ string) error {
	return nil
}

func nodeRestoreACLs(_ *data.Node, _ string) error {
	return nil
}

// A symlink's own permissions don't matter elsewhere (Linux has none).
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
