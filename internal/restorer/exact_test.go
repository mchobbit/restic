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
	"github.com/restic/restic/internal/fs"
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

func TestCloneSourcesKeepTheFirst(t *testing.T) {
	var c CloneSources
	id := restic.IDs{restic.NewRandomID(), restic.NewRandomID()}
	c.add(id, "a")
	c.add(id, "b")
	path, ok := c.Get(id)
	rtest.Assert(t, ok && path == "a", "got %q %v", path, ok)
	_, ok = c.Get(id[:1])
	rtest.Assert(t, !ok, "a prefix of the contents found")
	_, ok = c.Get(nil)
	rtest.Assert(t, !ok, "no contents found")
	rtest.Equals(t, 1, c.Len())

	var none *CloneSources
	_, ok = none.Get(id)
	rtest.Assert(t, !ok, "found in nil")
	rtest.Equals(t, 0, none.Len())
}

func TestPathBelow(t *testing.T) {
	tmp := rtest.TempDir(t)
	dir := filepath.Join(tmp, "at")
	dest := filepath.Join(dir, ".snapshots", "1", "snapshot")
	rtest.OK(t, os.MkdirAll(dest, 0o755))
	rel := filepath.Join(".snapshots", "1", "snapshot")

	for _, test := range []struct {
		name, dir, path string
		rel             string
		below           bool
	}{
		{"below", dir, dest, rel, true},
		{"itself", dir, dir, "", true},
		{"through ..", dir, filepath.Join(dir, ".snapshots", "..", ".snapshots", "1", "snapshot"), rel, true},
		{"beside", dir, filepath.Join(tmp, "atx"), "", false},
		{"above", dir, tmp, "", false},
		{"not there yet", dir, filepath.Join(dir, "new"), "new", true},
	} {
		got, below := pathBelow(test.dir, test.path)
		rtest.Assert(t, got == test.rel && below == test.below, "%v: got %q %v", test.name, got, below)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(tmp, "link")
		rtest.OK(t, os.Symlink(dir, link))
		got, below := pathBelow(link, dest)
		rtest.Assert(t, got == rel && below, "through a symlink: got %q %v", got, below)
	}
}

// contentOf is the content of a file of one blob with data.
func contentOf(data string) restic.IDs {
	return restic.IDs{restic.Hash([]byte(data))}
}

// NewCloneSources offers the files there, of their size, and none inside the
// target.
func TestNewCloneSources(t *testing.T) {
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"a": Dir{Nodes: map[string]Node{
				"f1": File{Data: "first"},
				"f2": File{Data: "second"},
				"f3": File{Data: "third"},
			}},
			"b":    Dir{Nodes: map[string]Node{"f1": File{Data: "first"}}},
			"dest": Dir{Nodes: map[string]Node{"f4": File{Data: "fourth"}}},
			"e":    File{Data: ""},
		},
	}, noopGetGenericAttributes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := rtest.TempDir(t)
	_, err := NewRestorer(repo, sn, Options{}).RestoreTo(ctx, dir)
	rtest.OK(t, err)
	// changed since: another size, and gone
	rtest.OK(t, os.WriteFile(filepath.Join(dir, "a", "f2"), []byte("2"), 0o644))
	rtest.OK(t, os.Remove(filepath.Join(dir, "a", "f3")))

	c, err := NewCloneSources(ctx, repo, *sn.Tree, dir, filepath.Join(dir, "dest"))
	rtest.OK(t, err)
	path, ok := c.Get(contentOf("first"))
	rtest.Assert(t, ok && path == filepath.Join(dir, "a", "f1"), "first: %q %v", path, ok)
	for _, data := range []string{"second", "third", "fourth"} {
		_, ok := c.Get(contentOf(data))
		rtest.Assert(t, !ok, "%v offered", data)
	}
	rtest.Equals(t, 1, c.Len())

	// the target is the directory itself: nothing
	c, err = NewCloneSources(ctx, repo, *sn.Tree, dir, dir)
	rtest.OK(t, err)
	rtest.Equals(t, 0, c.Len())
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

// --clone-from: a file whose contents the source has is cloned from there
// where the file system clones, and written where it doesn't; either way it
// comes back right.  Nothing inside the target is cloned from.
func TestRestoreExactCloneFrom(t *testing.T) {
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"live": Dir{Nodes: map[string]Node{
				"pkg": File{Data: "package v2"},
				"cfg": File{Data: "live config"},
			}},
			"snap": Dir{Nodes: map[string]Node{
				"pkg": File{Data: "package v2"},
				"cfg": File{Data: "old config!"},
			}},
		},
	}, noopGetGenericAttributes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	top := rtest.TempDir(t)
	_, err := NewRestorer(repo, sn, Options{}).RestoreTo(ctx, top)
	rtest.OK(t, err)

	liveTree, err := data.FindTreeDirectory(ctx, repo, sn.Tree, "/live")
	rtest.OK(t, err)
	snapTree, snapNode, err := data.FindTreeDirectoryNode(ctx, repo, sn.Tree, "/snap")
	rtest.OK(t, err)
	live := filepath.Join(top, "live")
	reflinks := fs.CloneFile(filepath.Join(live, "pkg"), filepath.Join(top, "probe"), int64(len("package v2"))) == nil
	rtest.OK(t, os.RemoveAll(filepath.Join(top, "probe")))

	// a new target, and one seeded with the live tree's other contents
	for _, seeded := range []bool{false, true} {
		dst := filepath.Join(rtest.TempDir(t), "snap")
		if seeded {
			rtest.OK(t, os.Mkdir(dst, 0o755))
			rtest.OK(t, os.WriteFile(filepath.Join(dst, "cfg"), []byte("live config"), 0o644))
		}
		sources, err := NewCloneSources(ctx, repo, *liveTree, live, dst)
		rtest.OK(t, err)
		rtest.Equals(t, 2, sources.Len())
		snapSn := *sn
		snapSn.Tree = snapTree
		res := NewRestorer(repo, &snapSn, Options{Exact: true})
		res.TargetNode = snapNode
		res.CloneSources = sources
		n, err := res.RestoreTo(ctx, dst)
		rtest.OK(t, err)
		_, err = res.VerifyFiles(ctx, dst, n, nil)
		rtest.OK(t, err)
		for name, want := range map[string]string{"pkg": "package v2", "cfg": "old config!"} {
			data, err := os.ReadFile(filepath.Join(dst, name))
			rtest.OK(t, err)
			rtest.Equals(t, want, string(data), name)
		}
		files, size := res.Cloned()
		if reflinks {
			rtest.Equals(t, uint64(1), files, "files cloned")
			rtest.Equals(t, uint64(len("package v2")), size, "bytes cloned")
		} else {
			rtest.Equals(t, uint64(0), files, "files cloned without reflinks")
		}
	}

	// the live tree restored over itself, cloning from itself: nothing is
	// offered, and nothing is emptied
	liveSn := *sn
	liveSn.Tree = liveTree
	sources, err := NewCloneSources(ctx, repo, *liveTree, live, live)
	rtest.OK(t, err)
	rtest.Equals(t, 0, sources.Len())
	res := NewRestorer(repo, &liveSn, Options{Exact: true})
	res.CloneSources = sources
	exactRestoreWith(t, res, live)
}
