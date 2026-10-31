package fs

import (
	"fmt"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
)

// UnsupportedError is metadata that the target file system can't hold: an
// inode flag, file flag, ZFS DOS flag or project ID refused with ENOTTY,
// EOPNOTSUPP or EINVAL, or ZFS DOS flags for a file system that isn't ZFS.  The restorer warns about it once per attribute and bits, and
// carries on: it is not a failed restore.  Every other failure stays an
// error.
type UnsupportedError struct {
	Attr data.GenericAttributeType
	Bits uint64 // the file flags refused; 0 for other attributes
	Err  error
}

func (e *UnsupportedError) Error() string {
	if e.Bits != 0 {
		return fmt.Sprintf("%v %#x not supported by the file system: %v", e.Attr, e.Bits, e.Err)
	}
	return fmt.Sprintf("%v not supported by the file system: %v", e.Attr, e.Err)
}

func (e *UnsupportedError) Unwrap() error {
	return e.Err
}

// FlagsWarning is a file flag left as it was for a reason that isn't an
// error: casefolding on a directory that already had entries.  The restorer
// prints it as a warning.
type FlagsWarning struct {
	Msg string
}

func (w *FlagsWarning) Error() string {
	return w.Msg
}

// SIDWarning is a SID that idmap couldn't map to an id on illumos (idmapd
// isn't running, say): the owner or group of a file (illumos.owner_sid,
// illumos.group_sid), or the user or group of an ACL entry (illumos.acl,
// illumos.xattr_meta), which got nobody (60001) instead.  Not the number
// the backup saw: that may be another SID's by now.  The restorer warns
// once per attribute, with the number of files at the end.
type SIDWarning struct {
	Attr data.GenericAttributeType
	SID  string
	Err  error
}

func (w *SIDWarning) Error() string {
	return fmt.Sprintf("%v %v not mapped by idmap, nobody (60001) used: %v", w.Attr, w.SID, w.Err)
}

func (w *SIDWarning) Unwrap() error {
	return w.Err
}

// IsWarning tells whether err is one the restorer only warns about: an
// UnsupportedError, a FlagsWarning, a SIDWarning or a
// data.MalformedAttributeError.
func IsWarning(err error) bool {
	var unsupported *UnsupportedError
	var flags *FlagsWarning
	var sid *SIDWarning
	var malformed *data.MalformedAttributeError
	return errors.As(err, &unsupported) || errors.As(err, &flags) || errors.As(err, &sid) ||
		errors.As(err, &malformed)
}

// SplitWarnings splits err, which may join several (errors.Join), into the
// warnings in it (IsWarning) and the rest.
func SplitWarnings(err error) (rest error, warnings []error) {
	if err == nil {
		return nil, nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var errs []error
		for _, e := range joined.Unwrap() {
			r, w := SplitWarnings(e)
			if r != nil {
				errs = append(errs, r)
			}
			warnings = append(warnings, w...)
		}
		return errors.Join(errs...), warnings
	}
	if IsWarning(err) {
		return nil, []error{err}
	}
	return err, nil
}
