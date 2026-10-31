package fs

// ZFS project IDs on illumos (zfs project), which ZFS's project quotas count
// a file's blocks against (zfs projectspace), and whether a directory hands
// its project ID down to the files created in it.  Plain Go, so that it is
// tested on any OS; the file access is in project_illumos.go.

// zfsProjInherit is the flag of a directory that hands its project ID down
// (zfs project -s; ZFS_PROJINHERIT_FL, Linux's FS_PROJINHERIT_FL).
const zfsProjInherit = 0x20000000

// zfsProject is a file's project ID and inheritance, as illumos.zfs_project
// keeps them.
type zfsProject struct {
	ID      uint32 `json:"id"`
	Inherit bool   `json:"inherit,omitempty"`
}

// zfsProjectOf returns the project of a file whose ZFS xattr flags and
// project ID are xflags and projid, and whether it has one worth keeping:
// an ID other than 0, or inheritance.
func zfsProjectOf(xflags, projid uint32) (zfsProject, bool) {
	p := zfsProject{ID: projid, Inherit: xflags&zfsProjInherit != 0}
	return p, p != zfsProject{}
}

// zfsProjectXflags returns xflags, a file's ZFS xattr flags, with
// inheritance as p has it and the other flags as they are.
func zfsProjectXflags(xflags uint32, p zfsProject) uint32 {
	if p.Inherit {
		return xflags | zfsProjInherit
	}
	return xflags &^ zfsProjInherit
}
