//go:build !illumos

package fs

// IsDoor tells whether fi is an illumos door (door_illumos.go): never, here.
func IsDoor(_ *ExtendedFileInfo) bool {
	return false
}
