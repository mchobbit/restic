package data

// MalformedAttributeError is a BackOtter attribute (file flags,
// linux.project_id, unix.sparse) whose value can't be decoded.  The restorer
// warns about it once per attribute and skips it: a snapshot that another
// build wrote oddly still restores, with everything else in it.
type MalformedAttributeError struct {
	Attr GenericAttributeType
	Err  error
}

func (e *MalformedAttributeError) Error() string {
	return e.Err.Error()
}

func (e *MalformedAttributeError) Unwrap() error {
	return e.Err
}

// Sparse reports whether node is a regular file that had holes when it was
// backed up (TypeUnixSparse).  The restorer writes such a file sparse, as
// with --sparse, when it writes it into a new file, and every other file
// fully allocated, so that a swap file or a preallocated one comes back with
// all its blocks.  false and a missing attribute both mean "not marked"; a
// malformed one does too, with a *MalformedAttributeError.
func (node *Node) Sparse() (bool, error) {
	var sparse bool
	ok, err := node.GenericAttribute(TypeUnixSparse, &sparse)
	if err != nil {
		return false, &MalformedAttributeError{Attr: TypeUnixSparse, Err: err}
	}
	return ok && sparse, nil
}

// SetSparse marks node as a regular file with holes.
func (node *Node) SetSparse() {
	if err := node.SetGenericAttribute(TypeUnixSparse, true); err != nil {
		panic(err) // a bool always marshals
	}
}

// ProjectID returns node's Linux project ID (TypeLinuxProjectID), and whether
// it has one other than 0: an explicit 0 is the same as none.
func (node *Node) ProjectID() (id uint32, ok bool, err error) {
	ok, err = node.GenericAttribute(TypeLinuxProjectID, &id)
	if err != nil {
		return 0, false, &MalformedAttributeError{Attr: TypeLinuxProjectID, Err: err}
	}
	return id, ok && id != 0, nil
}

// SetProjectID stores a Linux project ID.  Project ID 0, which every file
// has without project quotas, gets no attribute.
func (node *Node) SetProjectID(id uint32) {
	if id == 0 {
		return
	}
	if err := node.SetGenericAttribute(TypeLinuxProjectID, id); err != nil {
		panic(err) // a uint32 always marshals
	}
}
