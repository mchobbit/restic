package restorer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func exactTestNode(mode os.FileMode, xattrs ...string) *data.Node {
	n := &data.Node{
		Type:       data.NodeTypeFile,
		Mode:       mode,
		ModTime:    time.Unix(1_700_000_000, 5),
		AccessTime: time.Unix(1_700_000_100, 0),
		Size:       3,
		Inode:      1,
		Links:      1,
	}
	for i := 0; i+1 < len(xattrs); i += 2 {
		n.ExtendedAttributes = append(n.ExtendedAttributes, data.ExtendedAttribute{Name: xattrs[i], Value: []byte(xattrs[i+1])})
	}
	return n
}

// What a restore can't set, or doesn't, never keeps metadata from matching.
func TestSameMetadataIgnoresWhatARestoreCantSet(t *testing.T) {
	all := func(string) bool { return true }
	a := exactTestNode(0o644, "security.selinux", "x", "user.a", "1")
	b := exactTestNode(0o644, "user.a", "1", "security.selinux", "x")
	b.ChangeTime = time.Unix(1, 0)
	b.Inode, b.DeviceID, b.Links = 7, 9, 2
	b.User, b.Group = "root", "root"
	b.Content = restic.IDs{restic.NewRandomID()}
	rtest.Assert(t, sameMetadata(a, b, true, all), "differences a restore doesn't set")

	b.UID = 1
	rtest.Assert(t, !sameMetadata(a, b, true, all), "another owner, with owners")
	rtest.Assert(t, sameMetadata(a, b, false, all), "another owner, without owners")

	// an attribute not restored isn't compared
	c := exactTestNode(0o644, "security.selinux", "x", "user.a", "1", "user.skip", "1")
	rtest.Assert(t, sameMetadata(a, c, true, func(name string) bool { return name != "user.skip" }), "an attribute left out")

	// a node without an atime gets its mtime
	d, e := exactTestNode(0o644), exactTestNode(0o644)
	d.AccessTime = d.ModTime
	e.AccessTime = time.Time{}
	rtest.Assert(t, sameMetadata(d, e, true, all), "no atime: the mtime")
	// and one without an mtime keeps the times it has
	e.ModTime = time.Time{}
	d.ModTime = d.ModTime.Add(time.Hour)
	rtest.Assert(t, sameMetadata(d, e, true, all), "no times")

	// the same generic attributes, spaced otherwise
	f, g := exactTestNode(0o644), exactTestNode(0o644)
	f.GenericAttributes = map[data.GenericAttributeType]json.RawMessage{data.TypeLinuxInodeFlags: json.RawMessage(`{"flags":16}`)}
	g.GenericAttributes = map[data.GenericAttributeType]json.RawMessage{data.TypeLinuxInodeFlags: json.RawMessage(`{ "flags": 16 }`)}
	rtest.Assert(t, sameMetadata(f, g, true, all), "generic attributes spaced otherwise")
}

// Every difference a restore would set keeps metadata from matching.
func TestSameMetadataSeesEveryDifference(t *testing.T) {
	all := func(string) bool { return true }
	a := exactTestNode(0o644, "user.a", "1")
	for _, test := range []struct {
		name   string
		change func(n *data.Node)
	}{
		{"mode", func(n *data.Node) { n.Mode = 0o600 }},
		{"type", func(n *data.Node) { n.Type = data.NodeTypeSymlink }},
		{"mtime", func(n *data.Node) { n.ModTime = n.ModTime.Add(time.Nanosecond) }},
		{"atime", func(n *data.Node) { n.AccessTime = n.AccessTime.Add(time.Second) }},
		{"no atime", func(n *data.Node) { n.AccessTime = time.Time{} }},
		{"gid", func(n *data.Node) { n.GID = 5 }},
		{"size", func(n *data.Node) { n.Size = 4 }},
		{"xattr value", func(n *data.Node) { n.ExtendedAttributes[0].Value = []byte("2") }},
		{"no xattr", func(n *data.Node) { n.ExtendedAttributes = nil }},
		{"another xattr", func(n *data.Node) {
			n.ExtendedAttributes = append(n.ExtendedAttributes, data.ExtendedAttribute{Name: "security.selinux", Value: []byte("x")})
		}},
		{"generic attribute", func(n *data.Node) {
			n.GenericAttributes = map[data.GenericAttributeType]json.RawMessage{data.TypeLinuxInodeFlags: json.RawMessage(`{"flags":16}`)}
		}},
	} {
		b := exactTestNode(0o644, "user.a", "1")
		test.change(b)
		rtest.Assert(t, !sameMetadata(a, b, true, all), "%v: matches", test.name)
	}

	s, u := &data.Node{Type: data.NodeTypeSymlink, LinkTarget: "a"}, &data.Node{Type: data.NodeTypeSymlink, LinkTarget: "b"}
	rtest.Assert(t, !sameMetadata(s, u, true, all), "symlink targets")
	d, e := &data.Node{Type: data.NodeTypeCharDev, Device: 1}, &data.Node{Type: data.NodeTypeCharDev, Device: 2}
	rtest.Assert(t, !sameMetadata(d, e, true, all), "devices")
}

// exactRestore restores sn over dst with --exact, and verifies the files.
func exactRestore(t *testing.T, repo restic.Repository, sn *data.Snapshot, dst string) *Restorer {
	t.Helper()
	return exactRestoreWith(t, NewRestorer(repo, sn, Options{Exact: true}), dst)
}

// exactRestoreWith restores with res over dst, and verifies the files.
func exactRestoreWith(t *testing.T, res *Restorer, dst string) *Restorer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, err := res.RestoreTo(ctx, dst)
	rtest.OK(t, err)
	_, err = res.VerifyFiles(ctx, dst, n, nil)
	rtest.OK(t, err)
	return res
}

// --exact fixes a file of the same size and mtime with other contents,
// removes what the snapshot hasn't got, and sets metadata that differs.
func TestRestoreExact(t *testing.T) {
	mtime := time.Date(2026, 11, 10, 19, 23, 29, 0, time.UTC)
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"trick": File{Data: "AAAA", ModTime: mtime},
			"cfg":   File{Data: "cfg", Mode: 0o600, ModTime: mtime},
			"dir":   Dir{Nodes: map[string]Node{"f": File{Data: "f", ModTime: mtime}}},
		},
	}, noopGetGenericAttributes)
	dst := rtest.TempDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := NewRestorer(repo, sn, Options{}).RestoreTo(ctx, dst)
	rtest.OK(t, err)

	rtest.OK(t, os.WriteFile(filepath.Join(dst, "trick"), []byte("BBBB"), 0o644))
	rtest.OK(t, os.Chtimes(filepath.Join(dst, "trick"), mtime, mtime))
	rtest.OK(t, os.Chmod(filepath.Join(dst, "cfg"), 0o644))
	rtest.OK(t, os.WriteFile(filepath.Join(dst, "extra"), []byte("x"), 0o644))
	rtest.OK(t, os.MkdirAll(filepath.Join(dst, "dir", "sub"), 0o755))

	exactRestore(t, repo, sn, dst)
	data, err := os.ReadFile(filepath.Join(dst, "trick"))
	rtest.OK(t, err)
	rtest.Equals(t, "AAAA", string(data), "same size and mtime, other contents")
	for _, gone := range []string{"extra", filepath.Join("dir", "sub")} {
		_, err := os.Lstat(filepath.Join(dst, gone))
		rtest.Assert(t, os.IsNotExist(err), "%v not removed: %v", gone, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Lstat(filepath.Join(dst, "cfg"))
		rtest.OK(t, err)
		rtest.Equals(t, os.FileMode(0o600), fi.Mode().Perm())
	}
}
