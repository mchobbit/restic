package fs

import (
	"fmt"
	"syscall"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
)

// QuarantinedOpenError tells whether err, from opening the regular file at
// path for reading, is illumos refusing it because the file has the
// av_quarantined system attribute: EACCES, which root gets too.  Its node
// can still be kept, without its contents (NodeUnreadQuarantined).
func QuarantinedOpenError(path string, err error) bool {
	return errors.Is(err, syscall.EACCES) && isQuarantined(path)
}

// NodeUnreadQuarantined makes node, of a regular file that av_quarantined
// keeps closed, one without its contents (data.Node.SetUnread), and returns
// the warning about it, a *BackupWarning: the file is kept, the backup
// doesn't fail.
func NodeUnreadQuarantined(node *data.Node) error {
	size := node.Size
	node.SetUnread(size)
	return &BackupWarning{Msg: fmt.Sprintf("av_quarantined: contents not backed up (%d bytes)", size)}
}
