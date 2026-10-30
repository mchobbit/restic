//go:build !linux

package fs

import "github.com/restic/restic/internal/data"

// ZFS DOS flags are kept on Linux alone: FreeBSD's file flags and illumos'
// system attributes hold them elsewhere.
const zfsDosFlagsAttribute data.GenericAttributeType = ""

func restoreZFSDosFlags(_ string, _ data.NodeType, _ uint64) error {
	return nil
}
