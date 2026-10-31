//go:build !freebsd && !netbsd && !illumos

package fs

import "github.com/restic/restic/internal/data"

// No birth time can be set here (Linux has no call for it, OpenBSD and
// DragonFly have none to set); unix.birthtime is skipped, as a known
// attribute of another OS.
func nodeRestoreBirthTime(_ *data.Node, _ string) error {
	return nil
}
