package fs

import "slices"

// RemoveSELinuxLabel removes the SELinux label (security.selinux) of the
// file at path, if it has one: restore --exact, for a node without one,
// whose file got its label from the tree the destination was made from (a
// btrfs snapshot of a tree restored before) rather than the snapshot.
// Elsewhere a restore leaves a label a node lacks (xattrNeverRemoved).  On
// a system that enforces SELinux the removal fails (EACCES, even as root).
func RemoveSELinuxLabel(path string) error {
	names, err := listxattrFunc(path)
	if err != nil {
		return err
	}
	if !slices.Contains(names, selinuxLabel) {
		return nil
	}
	return removexattrFunc(path, selinuxLabel)
}
