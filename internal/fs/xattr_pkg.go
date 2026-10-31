//go:build darwin || freebsd || netbsd || linux || (solaris && !illumos)

package fs

import (
	"os"
	"syscall"

	"github.com/restic/restic/internal/errors"

	"github.com/pkg/xattr"
)

// getxattr retrieves extended attribute data associated with path.
func getxattr(path, name string) ([]byte, error) {
	b, err := xattr.LGet(path, name)
	return b, handleXattrErr(err)
}

// listxattr retrieves a list of names of extended attributes associated with the
// given path in the file system.
func listxattr(path string) ([]string, error) {
	l, err := xattr.LList(path)
	return l, handleXattrErr(err)
}

func isListxattrPermissionError(err error) bool {
	var xerr *xattr.Error
	if errors.As(err, &xerr) {
		return xerr.Op == "xattr.list" && errors.Is(xerr.Err, os.ErrPermission)
	}
	return false
}

// setxattr associates name and data together as an attribute of path.
// BackOtter: an attribute the file system doesn't support is an error that
// xattrUnsupported tells, for the restore to warn about, not nothing.
func setxattr(path, name string, data []byte) error {
	if err := xattr.LSet(path, name, data); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

// xattrUnsupported tells whether err, from setxattr, is one that
// handleXattrErr makes nothing: the file system doesn't support the
// attribute.
func xattrUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, xattr.ENOATTR)
}

// removexattr removes the attribute name from path.
func removexattr(path, name string) error {
	return handleXattrErr(xattr.LRemove(path, name))
}

func handleXattrErr(err error) error {
	switch e := err.(type) {
	case nil:
		return nil

	case *xattr.Error:
		// On Linux, xattr calls on files in an SMB/CIFS mount can return
		// ENOATTR instead of ENOTSUP.  BSD can return EOPNOTSUPP.
		if e.Err == syscall.ENOTSUP || e.Err == syscall.EOPNOTSUPP || e.Err == xattr.ENOATTR {
			return nil
		}
		return errors.WithStack(e)

	default:
		return errors.WithStack(e)
	}
}
