//go:build !freebsd && !linux

package fs

import "github.com/restic/restic/internal/data"

// No file flags are kept on this OS.
const fileFlagsAttribute data.GenericAttributeType = ""

//nolint:unused // not used on Windows
func nodeFillFileFlags(_ *data.Node, _ string, _ *ExtendedFileInfo) error {
	return nil
}

func restoreFileFlags(_ string, _ data.NodeType, _ uint32) error {
	return nil
}
