package fs

import (
	"errors"
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
