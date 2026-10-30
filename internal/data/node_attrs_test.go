package data

import (
	"encoding/json"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestNodeSparse(t *testing.T) {
	var node Node
	rtest.Assert(t, !node.Sparse(), "a node without the attribute is sparse")

	node.SetSparse()
	buf, err := json.Marshal(node.GenericAttributes)
	rtest.OK(t, err)
	rtest.Equals(t, `{"unix.sparse":true}`, string(buf))

	var back Node
	rtest.OK(t, json.Unmarshal(buf, &back.GenericAttributes))
	rtest.Assert(t, back.Sparse(), "sparse marker lost in JSON")

	back.GenericAttributes[TypeUnixSparse] = json.RawMessage(`false`)
	rtest.Assert(t, !back.Sparse(), "unix.sparse false taken for sparse")
	back.GenericAttributes[TypeUnixSparse] = json.RawMessage(`"yes"`)
	rtest.Assert(t, !back.Sparse(), "a malformed unix.sparse taken for sparse")
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

	back.GenericAttributes[TypeLinuxProjectID] = json.RawMessage(`-1`)
	_, _, err = back.ProjectID()
	rtest.Assert(t, err != nil, "no error for a negative project ID")
}
