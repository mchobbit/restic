//go:build illumos

package fs

import (
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

// nodeFillFileFlags records the system attributes of a regular file or
// directory, and its creation time (unix.birthtime), from the same view.
func nodeFillFileFlags(node *data.Node, path string, _ *ExtendedFileInfo) error {
	f, err := sysattrView(path, node.Type, false)
	if err != nil {
		// Not a file or directory, or a file system without them.
		debug.Log("no system attributes for %v: %v", path, err)
		return nil
	}
	defer func() { _ = f.Close() }()
	view, err := readSysattrs(f, path)
	if err != nil {
		return err
	}
	if crtime, ok := sysattrCrtime(view); ok {
		node.SetBirthTime(crtime)
	}
	if mask, ok := sysattrKept(view); ok {
		// 0 too, unlike file flags elsewhere (SetFileFlags).
		_ = node.SetGenericAttribute(fileFlagsAttribute, mask)
	}
	return nil
}

// readSysattrs reads the attributes in f, the SUNWattr_rw view of path, in
// one read(2): illumos packs the view afresh for each read, from its start
// whatever the offset, so io.ReadAll's second read, after 512 bytes, gave
// the start again for a view any longer (728 bytes for a file owned by a
// SID).  fstat(2) gives the view's size; the read may get more than that.
func readSysattrs(f *os.File, path string) (map[string]nvValue, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, &os.PathError{Op: "fstat SUNWattr_rw", Path: path, Err: err}
	}
	buf := make([]byte, max(fi.Size(), 0)+4096)
	n, err := f.Read(buf)
	if err != nil {
		return nil, &os.PathError{Op: "read SUNWattr_rw", Path: path, Err: err}
	}
	view, err := nvlistValues(buf[:n])
	if err != nil {
		return nil, &os.PathError{Op: "read SUNWattr_rw", Path: path, Err: err}
	}
	return view, nil
}

// restoreFileFlags sets the system attributes the view has to those in
// flags, writing their booleans alone (illumos_sysattr.go).  Each write
// names every attribute the view has, so one that can't be changed (by an
// owner who isn't root: nodump, appendonly, nounlink and immutable, and
// clearing av_modified, which writing the contents set) refuses the whole
// write; naming one without changing it needs no privilege.  So if flags
// can't be set at once, the attributes are changed one at a time from what
// the file has, and those refused keep their value.
func restoreFileFlags(path string, typ data.NodeType, flags uint32, _ []byte) error {
	f, err := sysattrView(path, typ, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	view, err := readSysattrs(f, path)
	if err != nil {
		return err
	}
	return changeBitsBestEffort(fileFlagsAttribute, "file flags", sysattrMask(view), flags, func(mask uint32) error {
		if _, err := f.WriteAt(sysattrMaskNvlist(view, mask), 0); err != nil {
			return &os.PathError{Op: "write SUNWattr_rw", Path: path, Err: err}
		}
		return nil
	})
}
