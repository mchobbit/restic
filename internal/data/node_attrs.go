package data

import "github.com/restic/restic/internal/debug"

// Sparse reports whether node is a regular file that had holes when it was
// backed up (TypeUnixSparse).  The restorer writes such a file sparse, as
// with --sparse, and every other file fully allocated, so that a swap file
// or a preallocated one comes back with all its blocks.
func (node *Node) Sparse() bool {
	var sparse bool
	ok, err := node.GenericAttribute(TypeUnixSparse, &sparse)
	if err != nil {
		debug.Log("ignoring %v of %v: %v", TypeUnixSparse, node.Name, err)
		return false
	}
	return ok && sparse
}

// SetSparse marks node as a regular file with holes.
func (node *Node) SetSparse() {
	if err := node.SetGenericAttribute(TypeUnixSparse, true); err != nil {
		panic(err) // a bool always marshals
	}
}

// ProjectID returns node's Linux project ID (TypeLinuxProjectID), and whether
// it has one other than 0.
func (node *Node) ProjectID() (id uint32, ok bool, err error) {
	ok, err = node.GenericAttribute(TypeLinuxProjectID, &id)
	if err != nil {
		return 0, false, err
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
