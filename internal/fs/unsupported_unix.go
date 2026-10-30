//go:build !windows

package fs

import (
	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// unsupportedErr tells the errors of a file system that lacks a kind of
// metadata, or can't answer a question about it: ENOTTY (no such ioctl),
// EOPNOTSUPP or ENOTSUP, and EINVAL.
func unsupportedErr(err error) bool {
	return errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL)
}
