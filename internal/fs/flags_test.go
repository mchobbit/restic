package fs

import (
	"errors"
	"syscall"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestSetFlagsBestEffort(t *testing.T) {
	// A file system that refuses bit 0x4, as ZFS refuses some FreeBSD flags.
	var current uint32
	set := func(f uint32) error {
		if f&0x4 != 0 {
			return syscall.EOPNOTSUPP
		}
		current = f
		return nil
	}

	rtest.OK(t, setFlagsBestEffort(0x3, set))
	rtest.Equals(t, uint32(0x3), current)

	current = 0
	err := setFlagsBestEffort(0x15, set)
	rtest.Assert(t, err != nil, "expected an error for the refused flag")
	rtest.Assert(t, errors.Is(err, syscall.EOPNOTSUPP), "error %v doesn't wrap EOPNOTSUPP", err)
	rtest.Equals(t, uint32(0x11), current)
}
