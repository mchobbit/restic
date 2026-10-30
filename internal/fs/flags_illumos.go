//go:build illumos

package fs

import (
	"io"
	"os"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"golang.org/x/sys/unix"
)

// On illumos the file flags are its system attributes (immutable, appendonly,
// nounlink, nodump, hidden, readonly, ...), as chmod(1)'s S+v sets them.  They
// are kept and restored like file flags elsewhere: set last, since immutable
// and appendonly refuse most other changes.  libc's fgetattr(3C) and
// fsetattr(3C) read and write them through each file's SUNWattr_rw extended
// attribute, a packed nvlist; restic does the same, without libnvpair.

const fileFlagsAttribute = data.TypeIllumosSystemAttributes

// sysattrView opens the SUNWattr_rw view of the file at path, a regular file
// or directory; other types of file have no system attributes worth keeping,
// and opening a device or FIFO could have side effects.
func sysattrView(path string, typ data.NodeType, write bool) (*os.File, error) {
	if typ != data.NodeTypeFile && typ != data.NodeTypeDir {
		return nil, unix.ENOTSUP
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	how := unix.O_XATTR | unix.O_RDONLY
	if write {
		how = unix.O_XATTR | unix.O_RDWR
	}
	vfd, err := unix.Openat(fd, "SUNWattr_rw", how, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat SUNWattr_rw", Path: path, Err: err}
	}
	return os.NewFile(uintptr(vfd), path+"/SUNWattr_rw"), nil
}

func nodeFillFileFlags(node *data.Node, path string, _ *ExtendedFileInfo) error {
	f, err := sysattrView(path, node.Type, false)
	if err != nil {
		// Not a file or directory, or a file system without them.
		debug.Log("no system attributes for %v: %v", path, err)
		return nil
	}
	defer func() { _ = f.Close() }()
	view, err := io.ReadAll(f)
	if err != nil {
		return &os.PathError{Op: "read SUNWattr_rw", Path: path, Err: err}
	}
	mask, err := sysattrMask(view)
	if err != nil {
		return &os.PathError{Op: "read SUNWattr_rw", Path: path, Err: err}
	}
	if mask&^illumosSysattrAutomatic == 0 {
		return nil
	}
	node.SetFileFlags(fileFlagsAttribute, mask)
	return nil
}

func restoreFileFlags(path string, typ data.NodeType, flags uint32, _ []byte) error {
	f, err := sysattrView(path, typ, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	view, err := io.ReadAll(f)
	if err != nil {
		return &os.PathError{Op: "read SUNWattr_rw", Path: path, Err: err}
	}
	return setFlagsBestEffort(flags, func(mask uint32) error {
		if err := setSysattrMask(view, mask); err != nil {
			return err
		}
		if _, err := f.WriteAt(view, 0); err != nil {
			return &os.PathError{Op: "write SUNWattr_rw", Path: path, Err: err}
		}
		return nil
	})
}
