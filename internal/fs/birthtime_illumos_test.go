//go:build illumos

package fs

import (
	"os"
	"testing"

	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// BackOtter: tmpfs refuses a crtime with EINVAL: no birth time to give.
func TestBirthTimeUnsupported(t *testing.T) {
	rtest.Assert(t, birthTimeUnsupported(&os.PathError{Op: "write", Path: "f", Err: unix.EINVAL}), "EINVAL")
	rtest.Assert(t, birthTimeUnsupported(unix.ENOTSUP), "ENOTSUP")
	rtest.Assert(t, !birthTimeUnsupported(unix.EPERM), "EPERM")
	rtest.Assert(t, !birthTimeUnsupported(nil), "nil")
}
