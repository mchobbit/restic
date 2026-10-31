package data

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	rtest "github.com/restic/restic/internal/test"
)

func TestNodeSparse(t *testing.T) {
	var node Node
	sparse, err := node.Sparse()
	rtest.OK(t, err)
	rtest.Assert(t, !sparse, "a node without the attribute is sparse")

	node.SetSparse()
	buf, err := json.Marshal(node.GenericAttributes)
	rtest.OK(t, err)
	rtest.Equals(t, `{"unix.sparse":true}`, string(buf))

	var back Node
	rtest.OK(t, json.Unmarshal(buf, &back.GenericAttributes))
	sparse, err = back.Sparse()
	rtest.OK(t, err)
	rtest.Assert(t, sparse, "sparse marker lost in JSON")

	back.GenericAttributes[TypeUnixSparse] = json.RawMessage(`false`)
	sparse, err = back.Sparse()
	rtest.OK(t, err)
	rtest.Assert(t, !sparse, "unix.sparse false taken for sparse")

	for _, raw := range []string{`"yes"`, `1`, `"x"`} {
		back.GenericAttributes[TypeUnixSparse] = json.RawMessage(raw)
		sparse, err = back.Sparse()
		rtest.Assert(t, !sparse, "a malformed unix.sparse %s taken for sparse", raw)
		var malformed *MalformedAttributeError
		rtest.Assert(t, errors.As(err, &malformed) && malformed.Attr == TypeUnixSparse,
			"unix.sparse %s: want a MalformedAttributeError, got %v", raw, err)
	}
}

func TestNodeProjectID(t *testing.T) {
	var node Node
	node.SetProjectID(0)
	rtest.Assert(t, node.GenericAttributes == nil, "project ID 0 got generic attributes")
	_, ok, err := node.ProjectID()
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "project ID without the attribute")

	node.SetProjectID(4294967295)
	buf, err := json.Marshal(node.GenericAttributes)
	rtest.OK(t, err)
	rtest.Equals(t, `{"linux.project_id":4294967295}`, string(buf))

	var back Node
	rtest.OK(t, json.Unmarshal(buf, &back.GenericAttributes))
	id, ok, err := back.ProjectID()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "project ID lost in JSON")
	rtest.Equals(t, uint32(4294967295), id)

	// An explicit 0 is the same as none: nothing to restore.
	back.GenericAttributes[TypeLinuxProjectID] = json.RawMessage(`0`)
	_, ok, err = back.ProjectID()
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "project ID 0 taken for one to restore")

	for _, raw := range []string{`-1`, `1.5`, `"x"`, `4294967296`} {
		back.GenericAttributes[TypeLinuxProjectID] = json.RawMessage(raw)
		_, ok, err = back.ProjectID()
		rtest.Assert(t, !ok, "malformed project ID %s taken", raw)
		var malformed *MalformedAttributeError
		rtest.Assert(t, errors.As(err, &malformed) && malformed.Attr == TypeLinuxProjectID,
			"project ID %s: want a MalformedAttributeError, got %v", raw, err)
	}
}

func TestNodeFileFlagsMalformed(t *testing.T) {
	var node Node
	for _, raw := range []string{`-1`, `1.5`, `"x"`} {
		node.GenericAttributes = map[GenericAttributeType]json.RawMessage{TypeLinuxInodeFlags: json.RawMessage(raw)}
		_, ok, err := node.FileFlags(TypeLinuxInodeFlags)
		rtest.Assert(t, !ok, "malformed file flags %s taken", raw)
		var malformed *MalformedAttributeError
		rtest.Assert(t, errors.As(err, &malformed) && malformed.Attr == TypeLinuxInodeFlags,
			"file flags %s: want a MalformedAttributeError, got %v", raw, err)
	}
}

func TestNodeZFSDosFlags(t *testing.T) {
	var node Node
	_, ok, err := node.ZFSDosFlags()
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "ZFS DOS flags without the attribute")

	// 0 is stored, and read back as a value: archive cleared.
	node.SetZFSDosFlags(0)
	buf, err := json.Marshal(node.GenericAttributes)
	rtest.OK(t, err)
	rtest.Equals(t, `{"linux.zfs_dos_flags":0}`, string(buf))
	var back Node
	rtest.OK(t, json.Unmarshal(buf, &back.GenericAttributes))
	flags, ok, err := back.ZFSDosFlags()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "ZFS DOS flags 0 lost in JSON")
	rtest.Equals(t, uint64(0), flags)

	// All the kept flags: a plain JSON number, below 2^53.
	node.SetZFSDosFlags(0x302f00000000)
	buf, err = json.Marshal(node.GenericAttributes)
	rtest.OK(t, err)
	rtest.Equals(t, `{"linux.zfs_dos_flags":52978421596160}`, string(buf))
	rtest.OK(t, json.Unmarshal(buf, &back.GenericAttributes))
	flags, ok, err = back.ZFSDosFlags()
	rtest.OK(t, err)
	rtest.Assert(t, ok, "ZFS DOS flags lost in JSON")
	rtest.Equals(t, uint64(0x302f00000000), flags)

	for _, raw := range []string{`-1`, `1.5`, `"x"`, `18446744073709551616`} {
		back.GenericAttributes[TypeLinuxZFSDosFlags] = json.RawMessage(raw)
		_, ok, err = back.ZFSDosFlags()
		rtest.Assert(t, !ok, "malformed ZFS DOS flags %s taken", raw)
		var malformed *MalformedAttributeError
		rtest.Assert(t, errors.As(err, &malformed) && malformed.Attr == TypeLinuxZFSDosFlags,
			"ZFS DOS flags %s: want a MalformedAttributeError, got %v", raw, err)
	}
}

func TestNodeBirthTime(t *testing.T) {
	var node Node
	_, ok, err := node.BirthTime()
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "birth time without the attribute")

	// Written in UTC with every nanosecond and no trailing zeros, as Go
	// writes a node's mtime and rustic-bko a jiff::Timestamp.
	for _, test := range []struct {
		birth time.Time
		json  string
	}{
		{time.Unix(1000000000, 123456789), `"2001-09-09T01:46:40.123456789Z"`},
		{time.Unix(1000000000, 0), `"2001-09-09T01:46:40Z"`},
		{time.Unix(1000000000, 5), `"2001-09-09T01:46:40.000000005Z"`},
		{time.Unix(1000000000, 5).In(time.FixedZone("CEST", 2*3600)), `"2001-09-09T01:46:40.000000005Z"`},
	} {
		var node Node
		node.SetBirthTime(test.birth)
		rtest.Equals(t, test.json, string(node.GenericAttributes[TypeUnixBirthTime]))

		birth, ok, err := node.BirthTime()
		rtest.OK(t, err)
		rtest.Assert(t, ok && birth.Equal(test.birth), "birth time %v came back as %v", test.birth, birth)
	}

	// Any offset and any number of fractional digits are read.
	node.GenericAttributes = map[GenericAttributeType]json.RawMessage{
		TypeUnixBirthTime: json.RawMessage(`"2001-09-09T03:46:40.5+02:00"`),
	}
	birth, ok, err := node.BirthTime()
	rtest.OK(t, err)
	rtest.Assert(t, ok && birth.Equal(time.Unix(1000000000, 500000000)), "birth time with an offset read as %v", birth)

	// What file systems without birth times report isn't kept.
	for _, sec := range []int64{0, -1} {
		var node Node
		node.SetBirthTime(time.Unix(sec, 0))
		node.SetBirthTime(time.Time{})
		rtest.Assert(t, node.GenericAttributes == nil, "birth time %d stored", sec)
	}

	for _, raw := range []string{`1000000000`, `"2001-09-09"`, `"x"`} {
		node.GenericAttributes[TypeUnixBirthTime] = json.RawMessage(raw)
		_, ok, err = node.BirthTime()
		rtest.Assert(t, !ok, "malformed birth time %s taken", raw)
		var malformed *MalformedAttributeError
		rtest.Assert(t, errors.As(err, &malformed) && malformed.Attr == TypeUnixBirthTime,
			"birth time %s: want a MalformedAttributeError, got %v", raw, err)
	}
	node.GenericAttributes[TypeUnixBirthTime] = json.RawMessage(`null`)
	_, ok, err = node.BirthTime()
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "birth time null taken")
}
