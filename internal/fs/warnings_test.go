package fs

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
)

func TestSplitWarningsSID(t *testing.T) {
	sid := &SIDWarning{Attr: data.TypeIllumosOwnerSID, SID: "S-1-5-21-1111-2222-3333-1001", Err: errors.New("idmap status -9961")}
	failed := errors.New("lchown failed")
	rest, warnings := SplitWarnings(errors.Join(failed, sid))
	rtest.Equals(t, failed.Error(), rest.Error())
	rtest.Equals(t, []error{sid}, warnings)
}

func TestSplitBackupWarnings(t *testing.T) {
	idmap := &BackupWarning{Msg: "idmap offline: SID owners not kept", Err: errors.New("idmap status -9993")}
	failed := errors.New("acl failed")
	rest, warnings := SplitBackupWarnings(errors.Join(failed, errors.Join(nil, idmap)))
	rtest.Equals(t, failed.Error(), rest.Error())
	rtest.Equals(t, []error{idmap}, warnings)
	rtest.Equals(t, "idmap offline: SID owners not kept: idmap status -9993", idmap.Error())
	rtest.Equals(t, "av_quarantined: contents not backed up (3 bytes)",
		(&BackupWarning{Msg: "av_quarantined: contents not backed up (3 bytes)"}).Error())

	// Alone, there is no error left.
	rest, warnings = SplitBackupWarnings(errors.Join(nil, idmap))
	rtest.Assert(t, rest == nil, "unexpected error %v", rest)
	rtest.Equals(t, []error{idmap}, warnings)

	// A restore's warnings aren't a backup's.
	sid := &SIDWarning{Attr: data.TypeIllumosOwnerSID, SID: "S-1-5-21-1111-2222-3333-1001", Err: errors.New("idmap status -9961")}
	rest, warnings = SplitBackupWarnings(sid)
	rtest.Equals(t, error(sid), rest)
	rtest.Equals(t, 0, len(warnings))
}

func TestNotRootErrors(t *testing.T) {
	path := "/restore/file"
	owner := &ownershipError{&os.LinkError{Op: "lchown", Old: path, New: path, Err: syscall.EPERM}}
	trusted := &os.PathError{Op: "setxattr trusted.bko", Path: path, Err: syscall.EPERM}
	system := &os.PathError{Op: "extattr_set_link system.bko", Path: path, Err: syscall.EACCES}
	failed := &os.PathError{Op: "utimes", Path: path, Err: syscall.EIO}
	unsupported := &UnsupportedError{Attr: data.TypeLinuxProjectID, Err: syscall.EOPNOTSUPP}

	// A failed lchown alone is ignored, as in stock restic.
	rtest.Assert(t, notRootErrors(path, errors.Join(owner)) == nil, "lchown's EPERM not ignored")

	// The permission errors after it are one warning naming each, the other
	// errors stay errors, and the other warnings stay as they are.
	err := notRootErrors(path, errors.Join(owner, trusted, errors.Join(system, unsupported), failed))
	rest, warnings := SplitWarnings(err)
	rtest.Equals(t, failed.Error(), rest.Error())
	rtest.Equals(t, 2, len(warnings))
	rtest.Equals(t, error(unsupported), warnings[0])
	var notRoot *NotRootWarning
	rtest.Assert(t, errors.As(warnings[1], &notRoot), "not a NotRootWarning: %v", warnings[1])
	rtest.Equals(t, []error{trusted, system}, notRoot.Errs)
	rtest.Equals(t, "/restore/file: not set, not running as root: setxattr trusted.bko /restore/file: "+
		"operation not permitted; extattr_set_link system.bko /restore/file: permission denied", notRoot.Error())

	// lchown failing for another reason stays an error.
	other := &ownershipError{&os.LinkError{Op: "lchown", Old: path, New: path, Err: syscall.EINVAL}}
	rest, warnings = SplitWarnings(notRootErrors(path, errors.Join(other, trusted)))
	rtest.Equals(t, other.Error(), rest.Error())
	rtest.Equals(t, 1, len(warnings))
}
