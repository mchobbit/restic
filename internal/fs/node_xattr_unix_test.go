//go:build darwin || freebsd || netbsd || linux || solaris

package fs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
)

// BackOtter: extended attributes are stored in the byte order of their
// names, whatever order the file system lists them in.
func TestFillExtendedAttributesSorted(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(file, []byte("hello world"), 0o600))
	names := []string{"user.zeta", "user.alpha", "user.mid", "user.Beta"}
	for _, name := range names {
		err := setxattr(file, name, []byte(name))
		if xattrUnsupported(err) {
			t.Skipf("file system without extended attributes: %v", err)
		}
		rtest.OK(t, err)
	}

	node := &data.Node{Type: data.NodeTypeFile}
	rtest.OK(t, nodeFillExtendedAttributes(node, file, false, t.Logf))
	if len(node.ExtendedAttributes) == 0 {
		t.Skip("file system without extended attributes")
	}
	var got []string
	for _, attr := range node.ExtendedAttributes {
		got = append(got, attr.Name)
	}
	rtest.Equals(t, []string{"user.Beta", "user.alpha", "user.mid", "user.zeta"}, got)
}

// BackOtter: a restore leaves the SELinux label of a file whose node has
// none alone, which SELinux never lets anyone remove; it removes the
// others.  Setting security.selinux takes root, and SELinux's own rules
// where it runs; the test is skipped where it can't.
func TestRestoreLeavesSELinuxLabel(t *testing.T) {
	rtest.Assert(t, xattrNeverRemoved("security.selinux"), "security.selinux removed")
	rtest.Assert(t, !xattrNeverRemoved("security.capability"), "security.capability kept")
	rtest.Assert(t, !xattrNeverRemoved("user.selinux"), "user.selinux kept")

	file := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(file, []byte("hello world"), 0o600))
	if err := setxattr(file, "user.old", []byte("old")); xattrUnsupported(err) {
		t.Skipf("file system without extended attributes: %v", err)
	}
	label := []byte("system_u:object_r:etc_t:s0\x00")
	if err := setxattr(file, selinuxLabel, label); err != nil {
		t.Skipf("can't set %v: %v", selinuxLabel, err)
	}
	node := &data.Node{Type: data.NodeTypeFile}
	rtest.OK(t, nodeRestoreExtendedAttributes(node, file, func(string) bool { return true }))
	names, err := listxattr(file)
	rtest.OK(t, err)
	rtest.Equals(t, []string{selinuxLabel}, names)
}

// BackOtter: a value larger than a page (60000 bytes; the 4 KiB a first
// read may try) is read whole.
func TestFillExtendedAttributesLargeValue(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(file, []byte("hello world"), 0o600))
	huge := make([]byte, 60000)
	for i := range huge {
		huge[i] = byte(i%251 + 1)
	}
	if err := setxattr(file, "user.huge", huge); err != nil {
		t.Skipf("file system without large extended attributes: %v", err)
	}
	node := &data.Node{Type: data.NodeTypeFile}
	rtest.OK(t, nodeFillExtendedAttributes(node, file, false, t.Logf))
	rtest.Equals(t, 1, len(node.ExtendedAttributes))
	rtest.Equals(t, huge, node.ExtendedAttributes[0].Value)
}

// BackOtter: an extended attribute the file has already, with its node's
// value, isn't set again (bcachefs refuses to set bcachefs.casefold again on
// a directory with entries, even to the value it has); the others are set,
// and those the node hasn't got removed.
func TestRestoreLeavesEqualXattrs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	rtest.OK(t, os.WriteFile(file, []byte("hello world"), 0o600))
	for name, value := range map[string]string{"user.same": "v", "user.diff": "old", "user.gone": "g", "user.empty": ""} {
		err := setxattr(file, name, []byte(value))
		if xattrUnsupported(err) {
			t.Skipf("file system without extended attributes: %v", err)
		}
		rtest.OK(t, err)
	}

	var set []string
	defer func(orig func(path, name string, data []byte) error) { setxattrFunc = orig }(setxattrFunc)
	setxattrFunc = func(path, name string, data []byte) error {
		set = append(set, name)
		return setxattr(path, name, data)
	}

	node := &data.Node{Type: data.NodeTypeFile, ExtendedAttributes: []data.ExtendedAttribute{
		{Name: "user.diff", Value: []byte("new")},
		{Name: "user.empty", Value: nil},
		{Name: "user.new", Value: []byte("n")},
		{Name: "user.same", Value: []byte("v")},
	}}
	rtest.OK(t, nodeRestoreExtendedAttributes(node, file, func(string) bool { return true }))
	slices.Sort(set)
	rtest.Equals(t, []string{"user.diff", "user.new"}, set)

	got := &data.Node{Type: data.NodeTypeFile}
	rtest.OK(t, nodeFillExtendedAttributes(got, file, false, t.Logf))
	rtest.Assert(t, got.Equals(*node), "xattrs %v, want %v", got.ExtendedAttributes, node.ExtendedAttributes)
}

// BackOtter: bcachefs refusing bcachefs.casefold on a directory with
// entries (ENOTEMPTY) is a warning, as chattr's F is (S6); the directory's
// other attributes are still set, and ENOTEMPTY for any other attribute
// stays an error.
func TestRestoreCasefoldNotEmptyIsWarning(t *testing.T) {
	dir := t.TempDir()
	if err := setxattr(dir, "user.probe", []byte("p")); xattrUnsupported(err) {
		t.Skipf("file system without extended attributes: %v", err)
	}

	defer func(orig func(path, name string, data []byte) error) { setxattrFunc = orig }(setxattrFunc)
	setxattrFunc = func(path, name string, value []byte) error {
		if name == bcachefsCasefold || name == "user.notempty" {
			return &os.PathError{Op: "setxattr " + name, Path: path, Err: syscall.ENOTEMPTY}
		}
		return setxattr(path, name, value)
	}

	all := func(string) bool { return true }
	node := &data.Node{Type: data.NodeTypeDir, ExtendedAttributes: []data.ExtendedAttribute{
		{Name: bcachefsCasefold, Value: []byte("1")},
		{Name: "user.other", Value: []byte("o")},
	}}
	err := nodeRestoreExtendedAttributes(node, dir, all)
	rtest.Assert(t, err != nil, "no warning")
	rtest.Assert(t, IsWarning(err), "not a warning: %v", err)
	rest, warnings := SplitWarnings(err)
	rtest.OK(t, rest)
	rtest.Equals(t, 1, len(warnings))
	rtest.Assert(t, strings.Contains(warnings[0].Error(), "casefolding (bcachefs.casefold) not restored on "+dir),
		"warning %q", warnings[0])
	value, err := getxattr(dir, "user.other")
	rtest.OK(t, err)
	rtest.Equals(t, "o", string(value))

	node.ExtendedAttributes = []data.ExtendedAttribute{{Name: "user.notempty", Value: []byte("1")}}
	err = nodeRestoreExtendedAttributes(node, dir, all)
	rtest.Assert(t, err != nil && !IsWarning(err), "ENOTEMPTY for user.notempty: %v", err)
}

// BackOtter: the same for removing bcachefs.casefold from a directory whose
// node hasn't got it: a warning, and the directory's other attributes are
// still set and removed; ENOTEMPTY removing any other attribute stays an
// error.
func TestRestoreCasefoldNotEmptyRemoveIsWarning(t *testing.T) {
	dir := t.TempDir()
	if err := setxattr(dir, "user.old", []byte("x")); xattrUnsupported(err) {
		t.Skipf("file system without extended attributes: %v", err)
	}

	// The directory lists the refused one too, which no file system here
	// can have.
	refused := bcachefsCasefold
	defer func(orig func(path string) ([]string, error)) { listxattrFunc = orig }(listxattrFunc)
	listxattrFunc = func(path string) ([]string, error) {
		names, err := listxattr(path)
		return append(names, refused), err
	}
	defer func(orig func(path, name string) error) { removexattrFunc = orig }(removexattrFunc)
	removexattrFunc = func(path, name string) error {
		if name == refused {
			return &os.PathError{Op: "removexattr " + name, Path: path, Err: syscall.ENOTEMPTY}
		}
		return removexattr(path, name)
	}

	all := func(string) bool { return true }
	node := &data.Node{Type: data.NodeTypeDir, ExtendedAttributes: []data.ExtendedAttribute{
		{Name: "user.new", Value: []byte("n")},
	}}
	err := nodeRestoreExtendedAttributes(node, dir, all)
	rtest.Assert(t, err != nil, "no warning")
	rtest.Assert(t, IsWarning(err), "not a warning: %v", err)
	rest, warnings := SplitWarnings(err)
	rtest.OK(t, rest)
	rtest.Equals(t, 1, len(warnings))
	rtest.Assert(t, strings.Contains(warnings[0].Error(), "casefolding (bcachefs.casefold) not restored on "+dir),
		"warning %q", warnings[0])
	names, err := listxattr(dir)
	rtest.OK(t, err)
	names = slices.DeleteFunc(names, func(name string) bool { return !strings.HasPrefix(name, "user.") })
	rtest.Equals(t, []string{"user.new"}, names)

	refused = "user.notempty"
	rtest.OK(t, setxattr(dir, "user.old", []byte("x")))
	err = nodeRestoreExtendedAttributes(node, dir, all)
	rtest.Assert(t, err != nil && !IsWarning(err), "ENOTEMPTY removing user.notempty: %v", err)
	names, err = listxattr(dir)
	rtest.OK(t, err)
	names = slices.DeleteFunc(names, func(name string) bool { return !strings.HasPrefix(name, "user.") })
	rtest.Equals(t, []string{"user.new"}, names)
}
