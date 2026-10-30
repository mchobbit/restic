package data

import (
	"encoding/json"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestNodeFileFlags(t *testing.T) {
	var node Node
	node.SetFileFlags(TypeFreeBSDFileFlags, 0)
	rtest.Assert(t, node.GenericAttributes == nil, "a file without flags got generic attributes")

	node.SetFileFlags(TypeFreeBSDFileFlags, 0x20002)
	buf, err := json.Marshal(node)
	rtest.OK(t, err)
	var back Node
	rtest.OK(t, json.Unmarshal(buf, &back))

	flags, ok, err := back.FileFlags(TypeFreeBSDFileFlags)
	rtest.OK(t, err)
	rtest.Assert(t, ok, "flags lost in JSON")
	rtest.Equals(t, uint32(0x20002), flags)

	_, ok, err = back.FileFlags(TypeLinuxInodeFlags)
	rtest.OK(t, err)
	rtest.Assert(t, !ok, "flags for another OS")
}
