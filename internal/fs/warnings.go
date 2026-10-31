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

// BackupWarning is metadata a backup couldn't keep for a reason that isn't
// the file's: on illumos, the SIDs of ephemeral owners, groups and ACL
// entries when idmap gives none (idmapd isn't running, say).  The fs
// package gives it once per backup; the archiver prints it as a warning,
// and the backup still succeeds.
type BackupWarning struct {
	Msg string
	Err error
}

func (w *BackupWarning) Error() string {
	return fmt.Sprintf("%v: %v", w.Msg, w.Err)
}

func (w *BackupWarning) Unwrap() error {
	return w.Err
}

// SplitWarnings splits err, which may join several (errors.Join), into the
// warnings in it (IsWarning) and the rest.
func SplitWarnings(err error) (rest error, warnings []error) {
	return splitErrors(err, IsWarning)
}

// SplitBackupWarnings splits err, from reading a file's metadata for a
// backup, which may join several (errors.Join), into the BackupWarnings in
// it and the rest.
func SplitBackupWarnings(err error) (rest error, warnings []error) {
	return splitErrors(err, func(err error) bool {
		var w *BackupWarning
		return errors.As(err, &w)
	})
}

// splitErrors splits err, which may join several (errors.Join), into those
// in it that warning picks and the rest.
func splitErrors(err error, warning func(error) bool) (rest error, warnings []error) {
	if err == nil {
		return nil, nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var errs []error
		for _, e := range joined.Unwrap() {
			r, w := splitErrors(e, warning)
			if r != nil {
				errs = append(errs, r)
			}
			warnings = append(warnings, w...)
		}
		return errors.Join(errs...), warnings
	}
	if warning(err) {
		return nil, []error{err}
	}
	return err, nil
}
