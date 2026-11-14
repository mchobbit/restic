//go:build !linux

package fs

import (
	"os"

	"github.com/restic/restic/internal/errors"
)

// errCloneUnsupported is cloneContents' error where reflinks aren't used.
var errCloneUnsupported = errors.New("reflinks not supported on this system")

// cloneContents fails: only Linux's FICLONE is used.
func cloneContents(dst, _ *os.File) error {
	return &os.PathError{Op: "clone", Path: dst.Name(), Err: errCloneUnsupported}
}
