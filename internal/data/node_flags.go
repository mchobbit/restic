package data

import (
	"encoding/json"

	"github.com/restic/restic/internal/errors"
)

// FileFlags returns the file flags node holds in the generic attribute typ,
// and whether it holds any.
func (node *Node) FileFlags(typ GenericAttributeType) (flags uint32, ok bool, err error) {
	raw, ok := node.GenericAttributes[typ]
	if !ok {
		return 0, false, nil
	}
	if err := json.Unmarshal(raw, &flags); err != nil {
		return 0, false, errors.Wrapf(err, "generic attribute %s", typ)
	}
	return flags, true, nil
}

// SetFileFlags stores file flags in the generic attribute typ.  A file without
// flags gets no attribute, so its node is the same as without flag support.
func (node *Node) SetFileFlags(typ GenericAttributeType, flags uint32) {
	if flags == 0 {
		return
	}
	raw, err := json.Marshal(flags)
	if err != nil {
		panic(err) // a uint32 always marshals
	}
	if node.GenericAttributes == nil {
		node.GenericAttributes = make(map[GenericAttributeType]json.RawMessage)
	}
	node.GenericAttributes[typ] = raw
}
