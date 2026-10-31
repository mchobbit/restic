package fs

import (
	"encoding/json"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestZFSProject(t *testing.T) {
	// What ZFS's ioctl gave on OmniOS: zfs project -p 77 -s, -p 79, -s alone.
	for _, test := range []struct {
		xflags, projid uint32
		json           string
	}{
		{0x20000000, 77, `{"id":77,"inherit":true}`},
		{0, 79, `{"id":79}`},
		{0x20000000, 0, `{"id":0,"inherit":true}`},
	} {
		p, ok := zfsProjectOf(test.xflags, test.projid)
		rtest.Assert(t, ok, "project %#x %d not kept", test.xflags, test.projid)
		buf, err := json.Marshal(p)
		rtest.OK(t, err)
		rtest.Equals(t, test.json, string(buf))
		var back zfsProject
		rtest.OK(t, json.Unmarshal(buf, &back))
		rtest.Equals(t, p, back)
		rtest.Equals(t, test.xflags, zfsProjectXflags(0, back))
	}
	_, ok := zfsProjectOf(0, 0)
	rtest.Assert(t, !ok, "project 0 without inheritance kept")

	// Other flags stay as they are.
	rtest.Equals(t, uint32(0x8), zfsProjectXflags(0x20000008, zfsProject{ID: 5}))
	rtest.Equals(t, uint32(0x20000008), zfsProjectXflags(0x8, zfsProject{ID: 5, Inherit: true}))
}
