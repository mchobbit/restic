package fs

import (
	"errors"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/data"
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

func TestSetFlagsBestEffortUnsupported(t *testing.T) {
	// Every refusal says unsupported: an UnsupportedError, for a warning.
	set := func(f uint32) error {
		if f&0x4 != 0 {
			return syscall.EOPNOTSUPP
		}
		return nil
	}
	err := setFlagsBestEffort(0x5, set)
	var unsupported *UnsupportedError
	if unsupportedErr(syscall.EOPNOTSUPP) {
		rtest.Assert(t, errors.As(err, &unsupported), "no UnsupportedError: %v", err)
		rtest.Equals(t, uint64(0x4), unsupported.Bits)
		rtest.Assert(t, IsWarning(err), "unsupported flags are an error")
	}

	// Another refusal too (no permission for the immutable flag): an
	// error, not a warning.
	set = func(f uint32) error {
		switch {
		case f&0x4 != 0:
			return syscall.EOPNOTSUPP
		case f&0x10 != 0:
			return syscall.EPERM
		}
		return nil
	}
	err = setFlagsBestEffort(0x15, set)
	rtest.Assert(t, err != nil, "no error")
	rtest.Assert(t, !errors.As(err, &unsupported), "refused for lack of permission, but an UnsupportedError: %v", err)
	rtest.Assert(t, errors.Is(err, syscall.EPERM), "error %v doesn't wrap EPERM", err)
	rtest.Assert(t, !IsWarning(err), "a refused flag is a warning")
}

func TestSplitWarnings(t *testing.T) {
	real1 := errors.New("real 1")
	real2 := errors.New("real 2")
	unsupported := &UnsupportedError{Attr: "linux.project_id", Err: syscall.EOPNOTSUPP}
	malformed := &data.MalformedAttributeError{Attr: data.TypeUnixSparse, Err: errors.New("bad")}
	flags := &FlagsWarning{Msg: "casefolding"}

	rest, warnings := SplitWarnings(nil)
	rtest.Assert(t, rest == nil && warnings == nil, "nil split into %v, %v", rest, warnings)

	rest, warnings = SplitWarnings(real1)
	rtest.Equals(t, real1, rest)
	rtest.Equals(t, 0, len(warnings))

	rest, warnings = SplitWarnings(errors.Join(real1, unsupported, errors.Join(malformed, real2), flags))
	rtest.Assert(t, errors.Is(rest, real1) && errors.Is(rest, real2), "real errors lost: %v", rest)
	rtest.Assert(t, !IsWarning(rest), "warnings left in %v", rest)
	rtest.Equals(t, 3, len(warnings))

	rest, warnings = SplitWarnings(errors.Join(unsupported))
	rtest.Assert(t, rest == nil, "rest of only warnings: %v", rest)
	rtest.Equals(t, 1, len(warnings))
}

func TestSetBitsBestEffortWide(t *testing.T) {
	// Flags above bit 31 (ZFS DOS flags) are tried one by one too.
	const high, refused = 0x200000000, 0x100000000000
	var current uint64
	set := func(f uint64) error {
		if f&refused != 0 {
			return syscall.EOPNOTSUPP
		}
		current = f
		return nil
	}
	err := setBitsBestEffort(data.TypeLinuxZFSDosFlags, "ZFS DOS flags", uint64(high|refused), set)
	var unsupported *UnsupportedError
	if unsupportedErr(syscall.EOPNOTSUPP) {
		rtest.Assert(t, errors.As(err, &unsupported), "no UnsupportedError: %v", err)
		rtest.Equals(t, uint64(refused), unsupported.Bits)
		rtest.Equals(t, data.TypeLinuxZFSDosFlags, unsupported.Attr)
	}
	rtest.Equals(t, uint64(high), current)

	// Clearing every flag refused: no bit to retry, but still reported.
	set = func(uint64) error { return syscall.EPERM }
	err = setBitsBestEffort(data.TypeLinuxZFSDosFlags, "ZFS DOS flags", uint64(0), set)
	rtest.Assert(t, errors.Is(err, syscall.EPERM), "error %v doesn't wrap EPERM", err)
	rtest.Assert(t, !IsWarning(err), "a refused flag is a warning")
}

func TestChangeBitsBestEffort(t *testing.T) {
	// illumos's system attributes as a file's owner who isn't root: every
	// write names all of them; immutable (0x20) can't be set, av_modified
	// (0x200) can't be cleared, everything else can be changed.  A new file
	// has archive and av_modified (0x208).
	const immutable, avModified = 0x20, 0x200
	var current uint32
	set := func(f uint32) error {
		if f&immutable != 0 && current&immutable == 0 ||
			f&avModified == 0 && current&avModified != 0 {
			return syscall.EPERM
		}
		current = f
		return nil
	}

	// archive, hidden, immutable, av_modified: all but immutable.
	current = 0x208
	err := changeBitsBestEffort(fileFlagsAttribute, "file flags", current, uint32(0x229), set)
	rtest.Assert(t, errors.Is(err, syscall.EPERM), "error %v doesn't wrap EPERM", err)
	rtest.Equals(t, "file flags 0x20 of 0x229 not set: operation not permitted", err.Error())
	rtest.Equals(t, uint32(0x209), current)

	// archive and av_modified cleared: archive is.
	current = 0x208
	err = changeBitsBestEffort(fileFlagsAttribute, "file flags", current, uint32(0), set)
	rtest.Equals(t, "file flags 0x200 not cleared: operation not permitted", err.Error())
	rtest.Equals(t, uint32(0x200), current)

	// hidden set and av_modified cleared, immutable set: hidden is.
	current = 0x208
	err = changeBitsBestEffort(fileFlagsAttribute, "file flags", current, uint32(0x29), set)
	rtest.Equals(t, "file flags 0x20 of 0x29 not set, 0x200 not cleared: operation not permitted", err.Error())
	rtest.Equals(t, uint32(0x209), current)

	// What the file has already: nothing to write.
	rtest.OK(t, changeBitsBestEffort(fileFlagsAttribute, "file flags", uint32(0x208), uint32(0x208),
		func(uint32) error { return syscall.EPERM }))

	// Settable as asked: no error, and one write.
	current = 0x208
	writes := 0
	rtest.OK(t, changeBitsBestEffort(fileFlagsAttribute, "file flags", current, uint32(0x20b), func(f uint32) error {
		writes++
		return set(f)
	}))
	rtest.Equals(t, uint32(0x20b), current)
	rtest.Equals(t, 1, writes)
}
