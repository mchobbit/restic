package data

import (
	"encoding/json"

	"github.com/restic/restic/internal/errors"
)

// GenericAttribute decodes the generic attribute typ into v, and reports
// whether node has that attribute.
func (node *Node) GenericAttribute(typ GenericAttributeType, v any) (bool, error) {
	raw, ok := node.GenericAttributes[typ]
	if !ok {
		return false, nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return false, errors.Wrapf(err, "generic attribute %s", typ)
	}
	return true, nil
}

// SetGenericAttribute stores v, encoded as JSON, as the generic attribute typ.
func (node *Node) SetGenericAttribute(typ GenericAttributeType, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return errors.Wrapf(err, "generic attribute %s", typ)
	}
	if node.GenericAttributes == nil {
		node.GenericAttributes = make(map[GenericAttributeType]json.RawMessage)
	}
	node.GenericAttributes[typ] = raw
	return nil
}

// FileFlags returns the file flags node holds in the generic attribute typ,
// and whether it holds any.
func (node *Node) FileFlags(typ GenericAttributeType) (flags uint32, ok bool, err error) {
	ok, err = node.GenericAttribute(typ, &flags)
	if err != nil {
		return 0, false, &MalformedAttributeError{Attr: typ, Err: err}
	}
	return flags, ok, nil
}

// SetFileFlags stores file flags in the generic attribute typ.  A file without
// flags gets no attribute, so its node is the same as without flag support.
func (node *Node) SetFileFlags(typ GenericAttributeType, flags uint32) {
	if flags == 0 {
		return
	}
	if err := node.SetGenericAttribute(typ, flags); err != nil {
		panic(err) // a uint32 always marshals
	}
}
