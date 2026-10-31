//go:build !linux && !freebsd && !darwin && !illumos

package fs

import (
	"io"

	"github.com/restic/restic/internal/data"
)

// SkipHoles returns f: without the SEEK_HOLE probe (seekhole_other.go), or
// on Windows, every file is read in full.
func SkipHoles(f File, _ *data.Node) io.Reader {
	return f
}
