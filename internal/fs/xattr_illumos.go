//go:build illumos

package fs

import (
	"io"
	"os"
	"slices"

	"github.com/restic/restic/internal/errors"
	"golang.org/x/sys/unix"
)

// illumos keeps extended attributes as named attribute files (runat(1),
// fsattr(5)) in a hidden directory of each file's own.  pkg/xattr reaches
// them only through a path it follows symlinks in, and its calls that don't
// follow them, which restic makes, fail with ENOTSUP; so they're read and
// written here with openat(2) and O_XATTR: those of regular files and
// directories, without the SUNWattr_ro and SUNWattr_rw views of the system
// attributes, which are kept as file flags (flags_illumos.go).

var sysattrViews = []string{"SUNWattr_ro", "SUNWattr_rw"}

// attrDir opens the attribute directory of the file at path.  It returns
// nil for other types of file, which have none worth keeping (and opening a
// device or FIFO could have side effects), and on a file system without
// them.
func attrDir(path string) (*os.File, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	if typ := st.Mode & unix.S_IFMT; typ != unix.S_IFREG && typ != unix.S_IFDIR {
		return nil, nil
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	afd, err := unix.Openat(fd, ".", unix.O_XATTR|unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, &os.PathError{Op: "openat O_XATTR", Path: path, Err: err}
	}
	return os.NewFile(uintptr(afd), path), nil
}

// listxattr returns the names of the extended attributes of the file at path.
func listxattr(path string) ([]string, error) {
	dir, err := attrDir(path)
	if dir == nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, &os.PathError{Op: "readdir O_XATTR", Path: path, Err: err}
	}
	names = slices.DeleteFunc(names, func(name string) bool { return slices.Contains(sysattrViews, name) })
	slices.Sort(names)
	return names, nil
}

func isListxattrPermissionError(err error) bool {
	return errors.Is(err, os.ErrPermission)
}

// openAttr opens the extended attribute name of the file at path.
func openAttr(path, name string, how int) (*os.File, error) {
	dir, err := attrDir(path)
	if dir == nil {
		if err == nil {
			err = &os.PathError{Op: "openat O_XATTR", Path: path, Err: unix.ENOTSUP}
		}
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	fd, err := unix.Openat(int(dir.Fd()), name, how|unix.O_CLOEXEC, 0o666)
	if err != nil {
		return nil, &os.PathError{Op: "openat " + name, Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path+"/@"+name), nil
}

// getxattr returns the value of the extended attribute name of path.
func getxattr(path, name string) ([]byte, error) {
	f, err := openAttr(path, name, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// setxattr sets the extended attribute name of path to data.  Like restic
// elsewhere, it does nothing on a file system without extended attributes.
func setxattr(path, name string, data []byte) error {
	f, err := openAttr(path, name, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC)
	if errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// removexattr removes the extended attribute name from path.
func removexattr(path, name string) error {
	dir, err := attrDir(path)
	if dir == nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
		return &os.PathError{Op: "unlinkat " + name, Path: path, Err: err}
	}
	return nil
}
