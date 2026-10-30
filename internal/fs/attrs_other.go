//go:build !freebsd && !netbsd && !illumos && !linux

package fs

import "github.com/restic/restic/internal/data"

// The ACLs and other attributes kept on FreeBSD and NetBSD (attrs_bsd.go),
// illumos (attrs_illumos.go) and Linux (attrs_linux.go) have no counterpart
// here.

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
