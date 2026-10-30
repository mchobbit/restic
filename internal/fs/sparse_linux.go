package fs

import "github.com/restic/restic/internal/data"

// On Linux, nodeFillFileFlags marks a file with holes, through the descriptor
// it opens for the flags and project ID anyway.
func nodeFillSparse(_ *data.Node, _ string, _ *ExtendedFileInfo) {}
