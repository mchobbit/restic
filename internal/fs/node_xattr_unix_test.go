//go:build darwin || freebsd || netbsd || linux || solaris

package fs

import (
	"os"
	"path/filepath"
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
