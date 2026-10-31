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
