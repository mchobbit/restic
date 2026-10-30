package fs

import (
	"encoding/hex"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

// The start of a real SUNWattr_rw view, from OmniOS r151058, of a file with
// nodump and immutable set (and archive and av_modified, as ZFS sets them),
// up to and including av_modified, then the end of the list.
const sunwattrRW = "010100000000000000000001000000200000002800000008726561646f6e6c79" +
	"00000015000000010000000000000020000000200000000668696464656e0000" +
	"00000015000000010000000000000020000000200000000673797374656d0000" +
	"0000001500000001000000000000002000000020000000076172636869766500" +
	"000000150000000100000001000000240000002800000009696d6d757461626c" +
	"650000000000001500000001000000010000002000000028000000086e6f756e" +
	"6c696e6b00000015000000010000000000000024000000280000000a61707065" +
	"6e646f6e6c790000000000150000000100000000000000200000002000000006" +
	"6e6f64756d70000000000015000000010000000100000028000000280000000e" +
	"61765f71756172616e74696e6564000000000015000000010000000000000024" +
	"000000280000000b61765f6d6f64696669656400000000150000000100000001" +
	"0000000000000000"

func TestSysattrMask(t *testing.T) {
	view, err := hex.DecodeString(sunwattrRW)
	rtest.OK(t, err)

	mask, err := sysattrMask(view)
	rtest.OK(t, err)
	want := illumosSysattrBits["nodump"] | illumosSysattrBits["immutable"] |
		illumosSysattrBits["archive"] | illumosSysattrBits["av_modified"]
	rtest.Equals(t, want, mask)

	// Clear immutable, set hidden, and read it back.
	want = want&^illumosSysattrBits["immutable"] | illumosSysattrBits["hidden"]
	rtest.OK(t, setSysattrMask(view, want))
	mask, err = sysattrMask(view)
	rtest.OK(t, err)
	rtest.Equals(t, want, mask)

	_, err = sysattrMask([]byte{0, 1, 0, 0})
	rtest.Assert(t, err != nil, "a native-encoded nvlist was accepted")
}
