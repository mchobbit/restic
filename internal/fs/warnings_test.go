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
