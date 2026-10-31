//go:build illumos

package fs

import (
	"errors"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestSIDNamerReportsOnce(t *testing.T) {
	sidsLost.Store(false)
	defer sidsLost.Store(false)

	rtest.Assert(t, (&sidNamer{}).report("/a") == nil, "report without a failure")

	// idmapd offline: a warning, not an error, and once per backup.
	s := &sidNamer{err: idmapStat(-9993)}
	err := s.report("/b")
	var w *BackupWarning
	rtest.Assert(t, errors.As(err, &w), "expected a *BackupWarning, got %v", err)
	rtest.Equals(t, "idmap offline: SID owners not kept: idmap status -9993", err.Error())
	rtest.Assert(t, s.report("/c") == nil, "second report")
	rtest.Assert(t, (&sidNamer{err: idmapStat(-9993)}).report("/d") == nil, "report for another file")
}
