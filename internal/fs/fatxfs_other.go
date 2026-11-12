//go:build !linux

package fs

import "github.com/restic/restic/internal/data"

// FAT attributes and XFS options are kept on Linux alone: FreeBSD's file
// flags hold FAT's attributes there (msdosfs), and no other system has XFS.
const (
	fatAttributesAttribute data.GenericAttributeType = ""
	xfsOptionsAttribute    data.GenericAttributeType = ""
)

func restoreFATAttributes(_ string, _ data.NodeType, _ uint32) error {
	return nil
}

func restoreXFSOptions(_ string, _ data.NodeType, _ data.XFSOptions) error {
	return nil
}

func prepareFileForXFSOptions(_ string, _ data.XFSOptions) {}

func clearDirXFSOptions(_ string) (data.XFSOptions, bool) {
	return data.XFSOptions{}, false
}
