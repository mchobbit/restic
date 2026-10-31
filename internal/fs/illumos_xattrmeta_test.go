package fs

import (
	"encoding/json"
	"testing"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

func TestXattrMetaJSON(t *testing.T) {
	metas := map[string]xattrMeta{
		"note": {
			UID: 1500, GID: 1501, Mode: 0o640,
			MTime: time.Unix(1000000000, 123456789).UTC(),
			ATime: time.Unix(1000000000, 0).UTC(),
			ACL: []aceEntry{
				{Who: 60001, Mask: 1},
				{Who: 2147483650, Mask: 2, Flags: aceIdentifierGroup, Type: aceAccessDenied, SID: "S-1-5-21-1111-2222-3333-513"},
			},
		},
		"a name with spaces": {UID: 0, GID: 0, Mode: 0o600,
			MTime: time.Unix(1000000000, 5).UTC(), ATime: time.Unix(1000000000, 5).UTC()},
		"Grüße, ☃ und 日本語": {UID: 1, GID: 2, Mode: 0o4755,
			MTime: time.Unix(1900000000, 0).UTC(), ATime: time.Unix(1, 0).UTC()},
	}
	buf, err := json.Marshal(metas)
	rtest.OK(t, err)
	var back map[string]xattrMeta
	rtest.OK(t, json.Unmarshal(buf, &back))
	rtest.Equals(t, metas, back)

	// The form both tools write: the names as they are, times as a node's
	// mtime, no "acl" for a trivial one.
	buf, err = json.Marshal(map[string]xattrMeta{"a name with spaces": metas["a name with spaces"], "note": metas["note"]})
	rtest.OK(t, err)
	const want = `{"a name with spaces":{"uid":0,"gid":0,"mode":384,` +
		`"mtime":"2001-09-09T01:46:40.000000005Z","atime":"2001-09-09T01:46:40.000000005Z"},` +
		`"note":{"uid":1500,"gid":1501,"mode":416,` +
		`"mtime":"2001-09-09T01:46:40.123456789Z","atime":"2001-09-09T01:46:40Z",` +
		`"acl":[{"who":60001,"mask":1},{"who":2147483650,"mask":2,"flags":64,"type":1,"sid":"S-1-5-21-1111-2222-3333-513"}]}}`
	rtest.Equals(t, want, string(buf))

	// Any offset is read.
	var meta xattrMeta
	rtest.OK(t, json.Unmarshal([]byte(`{"uid":1,"gid":2,"mode":420,"mtime":"2001-09-09T03:46:40.5+02:00","atime":"2001-09-09T01:46:40Z"}`), &meta))
	rtest.Assert(t, meta.MTime.Equal(time.Unix(1000000000, 500000000)), "mtime %v", meta.MTime)
}
