//go:build !linux

package fs

import "github.com/restic/restic/internal/data"

// PrepareDirForOptions does nothing here: only bcachefs (Linux,
// attrs_linux.go) passes a directory's options to the entries it gets
// after they are set.
func PrepareDirForOptions(_ string, _ *data.Node, _ func(xattrName string) bool) {}
