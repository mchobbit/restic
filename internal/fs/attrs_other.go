//go:build !freebsd && !illumos

package fs

import "github.com/restic/restic/internal/data"

// The ACLs and other attributes kept on FreeBSD (attrs_freebsd.go) and
// illumos (attrs_illumos.go) have no counterpart here.

//nolint:unused // not used on Windows
func nodeFillOSAttributes(_ *data.Node, _ string, _ *ExtendedFileInfo) error {
	return nil
}

//nolint:unused // not used on Windows
func nodeRestoreOSAttributes(_ *data.Node, _ string) error {
	return nil
}

func nodeRestoreACLs(_ *data.Node, _ string) error {
	return nil
}

// A symlink's own permissions don't matter elsewhere (Linux has none).
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
