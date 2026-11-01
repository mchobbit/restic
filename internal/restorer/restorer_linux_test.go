package restorer

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
	"golang.org/x/sys/unix"
)

// attributesFor returns a getGenericAttributes for saveSnapshot that gives
// each FileAttributes in attrs (by identity) its generic attributes.
func attributesFor(attrs map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage) func(*FileAttributes, bool) map[data.GenericAttributeType]json.RawMessage {
	return func(attr *FileAttributes, _ bool) map[data.GenericAttributeType]json.RawMessage {
		return attrs[attr]
	}
}

// A file whose contents are right already, but that has holes it didn't
// have when it was backed up (a swap file from an interrupted restore), is
// left as it is but fully allocated.
func TestRestorerFillHoles(t *testing.T) {
	zeros := string(make([]byte, chunker.MinSize))
	content := "start" + zeros + zeros + "end"
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"file": File{DataParts: []string{"start", zeros, zeros, "end"}},
		},
	}, noopGetGenericAttributes)

	dst := rtest.TempDir(t)
	target := filepath.Join(dst, "file")
	f, err := os.Create(target)
	rtest.OK(t, err)
	_, err = f.WriteAt([]byte("start"), 0)
	rtest.OK(t, err)
	_, err = f.WriteAt([]byte("end"), int64(len(content)-3))
	rtest.OK(t, err)
	if err := fs.PreallocateFile(f, 1); err != nil {
		t.Skipf("no preallocation here: %v", err)
	}
	rtest.OK(t, f.Close())
	if getBlockCount(t, target)*512 >= int64(len(content)) {
		t.Skip("no holes on this file system")
	}

	res := NewRestorer(repo, sn, Options{Overwrite: OverwriteIfChanged})
	_, err = res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)
	restored, err := os.ReadFile(target)
	rtest.OK(t, err)
	rtest.Assert(t, string(restored) == content, "wrong content")
	blocks := getBlockCount(t, target)
	rtest.Assert(t, blocks*512 >= int64(len(content)), "holes left: %d blocks for %d bytes", blocks, len(content))
}

// casefold is the flags attribute of a casefolded directory.
var casefold = map[data.GenericAttributeType]json.RawMessage{data.TypeLinuxInodeFlags: json.RawMessage(`1073741824`)}

// The restorer makes every directory's casefolding that of its node while
// it is empty, before anything goes in: as it creates it, including the
// unselected parents of --include'd entries and an "ID:subfolder" target.
func TestRestorerCasefoldOrder(t *testing.T) {
	type call struct {
		path  string
		flags uint32
		empty bool
	}
	var calls []call
	defer func(prepare func(string, uint32) error) { prepareDirForFlags = prepare }(prepareDirForFlags)
	prepareDirForFlags = func(path string, flags uint32) error {
		entries, err := os.ReadDir(path)
		rtest.OK(t, err)
		calls = append(calls, call{path, flags, len(entries) == 0})
		return nil
	}
	// BackOtter: the bcachefs options go on at the same time, from the
	// directory's own node.
	var optionCalls []call
	defer func(prepare func(string, *data.Node, func(string) bool)) { prepareDirForOptions = prepare }(prepareDirForOptions)
	prepareDirForOptions = func(path string, node *data.Node, _ func(string) bool) {
		entries, err := os.ReadDir(path)
		rtest.OK(t, err)
		rtest.Assert(t, node != nil && node.Type == data.NodeTypeDir, "%v: node %v", path, node)
		flags, _, _ := fs.NodeFileFlags(node)
		optionCalls = append(optionCalls, call{path, flags, len(entries) == 0})
	}

	folded := &FileAttributes{}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"folded": Dir{attributes: folded, Nodes: map[string]Node{
				"plain": Dir{Nodes: map[string]Node{
					"file": File{Data: "data"},
				}},
			}},
		},
	}, attributesFor(map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage{folded: casefold}))
	ctx := context.TODO()
	check := func(dst string, want []call) {
		t.Helper()
		for i := range want {
			want[i].path = filepath.Join(dst, want[i].path)
			want[i].empty = true
		}
		rtest.Equals(t, want, calls)
		rtest.Equals(t, want, optionCalls)
		calls, optionCalls = nil, nil
	}

	// A whole snapshot: every directory but the target.
	dst := rtest.TempDir(t)
	_, err := NewRestorer(repo, sn, Options{}).RestoreTo(ctx, dst)
	rtest.OK(t, err)
	check(dst, []call{{"folded", 0x40000000, true}, {"folded/plain", 0, true}})

	// Again: existing directories with entries are left alone.
	_, err = NewRestorer(repo, sn, Options{}).RestoreTo(ctx, dst)
	rtest.OK(t, err)
	check(dst, nil)

	// Only the file: its unselected parents too, outermost first.
	dst = rtest.TempDir(t)
	res := NewRestorer(repo, sn, Options{})
	res.SelectFilter = func(item string, _ bool) (bool, bool) {
		return item == "/folded/plain/file", strings.HasPrefix("/folded/plain/file", item)
	}
	_, err = res.RestoreTo(ctx, dst)
	rtest.OK(t, err)
	check(dst, []call{{"folded", 0x40000000, true}, {"folded/plain", 0, true}})
	_, err = os.Stat(filepath.Join(dst, "folded/plain/file"))
	rtest.OK(t, err)

	// "ID:folded" into an empty target: the target too.
	subtree, node, err := data.FindTreeDirectoryNode(ctx, repo, sn.Tree, "/folded")
	rtest.OK(t, err)
	sub := *sn
	sub.Tree = subtree
	dst = filepath.Join(rtest.TempDir(t), "target")
	res = NewRestorer(repo, &sub, Options{})
	res.TargetNode = node
	_, err = res.RestoreTo(ctx, dst)
	rtest.OK(t, err)
	check(dst, []call{{"", 0x40000000, true}, {"plain", 0, true}})
}

// Malformed BackOtter attributes are each a warning once, not an error, and
// a project ID the target file system can't hold is a warning once, with
// the number of files at the end.
func TestRestorerAttributeWarnings(t *testing.T) {
	malformed := &FileAttributes{}
	project := &FileAttributes{}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"m1": File{Data: "data", attributes: malformed},
			"m2": File{Data: "data", attributes: malformed},
			"p1": File{Data: "data", attributes: project},
			"p2": File{Data: "data", attributes: project},
			"p3": File{Data: "data", attributes: project},
		},
	}, attributesFor(map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage{
		malformed: {
			data.TypeUnixSparse:       json.RawMessage(`"x"`),
			data.TypeLinuxProjectID:   json.RawMessage(`-1`),
			data.TypeLinuxInodeFlags:  json.RawMessage(`1.5`),
			data.TypeLinuxZFSDosFlags: json.RawMessage(`"x"`),
		},
		project: {data.TypeLinuxProjectID: json.RawMessage(`7`)},
	}))

	dst := rtest.TempDir(t)
	res := NewRestorer(repo, sn, Options{})
	var warnings []string
	res.Warn = func(msg string) { warnings = append(warnings, msg) }
	_, err := res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)

	count := func(substr string) int {
		n := 0
		for _, w := range warnings {
			if strings.Contains(w, substr) {
				n++
			}
		}
		return n
	}
	for _, attr := range []string{"ignoring unix.sparse", "ignoring linux.project_id", "ignoring linux.inode_flags", "ignoring linux.zfs_dos_flags"} {
		rtest.Assert(t, count(attr) == 1, "want one warning %q, got %q", attr, warnings)
	}
	unsupported := count("linux.project_id not supported")
	if unsupported == 0 {
		t.Logf("project IDs supported here: %q", warnings)
		return
	}
	rtest.Assert(t, unsupported == 2 && count("3 files in all") == 1, "want a warning and a total, got %q", warnings)
}

// ZFS DOS flags restored onto another file system are a warning once, with
// the number of files at the end, once per hard-linked file, and the rest of
// the files' metadata is restored.
func TestRestorerZFSDosFlagsNotZFS(t *testing.T) {
	dst := rtest.TempDir(t)
	var st unix.Statfs_t
	rtest.OK(t, unix.Statfs(dst, &st))
	if uint32(st.Type) == 0x2fc12fc1 {
		t.Skip("the temporary directory is on ZFS")
	}
	hidden := &FileAttributes{}
	cleared := &FileAttributes{}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"h1":  File{Data: "data", attributes: hidden, Mode: 0o640},
			"h2":  File{Data: "data", attributes: hidden, Inode: 7, Links: 2},
			"h2b": File{Data: "data", attributes: hidden, Inode: 7, Links: 2},
			"c":   File{Data: "data", attributes: cleared},
			"d":   Dir{attributes: hidden},
		},
	}, attributesFor(map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage{
		hidden:  {data.TypeLinuxZFSDosFlags: json.RawMessage(`8589934592`)},
		cleared: {data.TypeLinuxZFSDosFlags: json.RawMessage(`0`)},
	}))

	res := NewRestorer(repo, sn, Options{})
	var warnings []string
	res.Warn = func(msg string) { warnings = append(warnings, msg) }
	_, err := res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)
	rtest.Equals(t, 2, len(warnings))
	rtest.Assert(t, strings.Contains(warnings[0], "linux.zfs_dos_flags not supported by the target file system: not ZFS"), "want a not ZFS warning, got %q", warnings)
	rtest.Assert(t, strings.Contains(warnings[1], "4 files in all"), "want a total of 4 files, got %q", warnings)
	fi, err := os.Stat(filepath.Join(dst, "h1"))
	rtest.OK(t, err)
	rtest.Equals(t, os.FileMode(0o640), fi.Mode().Perm())
}

// With root, on a loop ext4 with casefolding and project IDs: a casefolded
// directory is created casefolded, a directory without casefolding below it
// comes back without it and holds both "Makefile" and "makefile", and a
// project ID goes on before the immutable flag.
func TestRestorerExt4CasefoldProject(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, to mount a file system")
	}
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("no mkfs.ext4")
	}
	tmp := rtest.TempDir(t)
	img := filepath.Join(tmp, "ext4.img")
	mnt := filepath.Join(tmp, "mnt")
	rtest.OK(t, os.Mkdir(mnt, 0o755))
	rtest.OK(t, os.WriteFile(img, nil, 0o600))
	rtest.OK(t, os.Truncate(img, 64<<20))
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "casefold,project", img).CombinedOutput(); err != nil {
		t.Skipf("mkfs.ext4 -O casefold,project: %v: %s", err, out)
	}
	if out, err := exec.Command("mount", "-o", "loop", img, mnt).CombinedOutput(); err != nil {
		t.Skipf("mount: %v: %s", err, out)
	}
	defer func() {
		_ = exec.Command("chattr", "-i", filepath.Join(mnt, "dst", "proj")).Run()
		_ = exec.Command("umount", mnt).Run()
	}()

	folded := &FileAttributes{}
	proj := &FileAttributes{}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"folded": Dir{attributes: folded, Nodes: map[string]Node{
				"Readme": File{Data: "readme"},
				"plain": Dir{Nodes: map[string]Node{
					"Makefile": File{Data: "upper"},
					"makefile": File{Data: "lower"},
				}},
			}},
			"proj": File{Data: "project", attributes: proj},
		},
	}, attributesFor(map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage{
		folded: casefold,
		proj: {
			data.TypeLinuxProjectID:  json.RawMessage(`7`),
			data.TypeLinuxInodeFlags: json.RawMessage(`16`), // FS_IMMUTABLE_FL
		},
	}))

	dst := filepath.Join(mnt, "dst")
	res := NewRestorer(repo, sn, Options{})
	res.Warn = func(msg string) { t.Errorf("warning: %v", msg) }
	_, err := res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)

	flagsOf := func(path string) uint32 {
		f, err := os.Open(path)
		rtest.OK(t, err)
		defer func() { _ = f.Close() }()
		flags, err := unix.IoctlGetUint32(int(f.Fd()), unix.FS_IOC_GETFLAGS)
		rtest.OK(t, err)
		return flags
	}
	rtest.Assert(t, flagsOf(filepath.Join(dst, "folded"))&0x40000000 != 0, "casefolding not set")
	rtest.Assert(t, flagsOf(filepath.Join(dst, "folded/plain"))&0x40000000 == 0, "casefolding inherited")
	for name, want := range map[string]string{"Makefile": "upper", "makefile": "lower"} {
		got, err := os.ReadFile(filepath.Join(dst, "folded/plain", name))
		rtest.OK(t, err)
		rtest.Equals(t, want, string(got))
	}
	got, err := os.ReadFile(filepath.Join(dst, "folded/README"))
	rtest.OK(t, err)
	rtest.Equals(t, "readme", string(got))

	rtest.Assert(t, flagsOf(filepath.Join(dst, "proj"))&0x10 != 0, "immutable not set")
	out, err := exec.Command("lsattr", "-p", filepath.Join(dst, "proj")).Output()
	rtest.OK(t, err)
	rtest.Equals(t, "7", strings.Fields(string(out))[0])
}

// A restore over files that are immutable or append-only (chattr +i, +a)
// clears those flags first, so that their contents and metadata come back,
// and then sets each node's flags (BackOtter; rustic-bko does the same).
func TestRestorerOverRestrictingFlags(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (CAP_LINUX_IMMUTABLE)")
	}
	dst := rtest.TempDir(t)
	for name, flag := range map[string]string{"imm": "+i", "app": "+a", "plain": "+i"} {
		path := filepath.Join(dst, name)
		rtest.OK(t, os.WriteFile(path, []byte("changed contents"), 0o644))
		if out, err := exec.Command("chattr", flag, path).CombinedOutput(); err != nil {
			t.Skipf("chattr %v: %v: %s", flag, err, out)
		}
	}
	defer func() {
		for _, name := range []string{"imm", "app", "plain"} {
			_ = exec.Command("chattr", "-i", "-a", filepath.Join(dst, name)).Run()
		}
	}()

	imm, app := &FileAttributes{}, &FileAttributes{}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{
		Nodes: map[string]Node{
			"imm":   File{Data: "immutable", attributes: imm},
			"app":   File{Data: "append-only", attributes: app},
			"plain": File{Data: "no flags"},
		},
	}, attributesFor(map[*FileAttributes]map[data.GenericAttributeType]json.RawMessage{
		imm: {data.TypeLinuxInodeFlags: json.RawMessage(`16`)}, // FS_IMMUTABLE_FL
		app: {data.TypeLinuxInodeFlags: json.RawMessage(`32`)}, // FS_APPEND_FL
	}))

	res := NewRestorer(repo, sn, Options{})
	res.Error = func(location string, err error) error {
		t.Errorf("restore %v: %v", location, err)
		return nil
	}
	_, err := res.RestoreTo(context.TODO(), dst)
	rtest.OK(t, err)

	flagsOf := func(path string) uint32 {
		f, err := os.Open(path)
		rtest.OK(t, err)
		defer func() { _ = f.Close() }()
		flags, err := unix.IoctlGetUint32(int(f.Fd()), unix.FS_IOC_GETFLAGS)
		rtest.OK(t, err)
		return flags
	}
	for name, want := range map[string]struct {
		data  string
		flags uint32
	}{"imm": {"immutable", 0x10}, "app": {"append-only", 0x20}, "plain": {"no flags", 0}} {
		path := filepath.Join(dst, name)
		got, err := os.ReadFile(path)
		rtest.OK(t, err)
		rtest.Equals(t, want.data, string(got))
		rtest.Equals(t, want.flags, flagsOf(path)&0x30)
	}
}
