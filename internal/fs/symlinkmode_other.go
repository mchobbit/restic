//go:build !freebsd && !openbsd && !netbsd && !dragonfly && !illumos && !linux

package fs

import "github.com/restic/restic/internal/data"

// A symlink's own permissions don't matter elsewhere.
func nodeRestoreSymlinkMode(_ *data.Node, _ string) error {
	return nil
}
