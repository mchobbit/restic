//go:build !linux

package fs

import "github.com/restic/restic/internal/data"

// PrepareDirForOptions does nothing here: only bcachefs (Linux,
// attrs_linux.go) passes a directory's options to the entries it gets
// after they are set.
func PrepareDirForOptions(_ string, _ *data.Node, _ func(xattrName string) bool, _ bool) {}

// RestoreZeroProjectID does nothing here: only bcachefs (Linux,
// attrs_linux.go) hands a directory's project ID to the entries created
// in it whatever its flags.
func RestoreZeroProjectID(_ string, _ data.NodeType) {}
