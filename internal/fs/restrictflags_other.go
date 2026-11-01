//go:build !(freebsd || netbsd || openbsd || dragonfly || linux)

package fs

// ClearRestrictingFlags does nothing here: no flags BackOtter keeps keep a
// restore from changing a file (illumos' immutable, appendonly and nounlink
// system attributes are left as they are: see BACKOTTER.md).
func ClearRestrictingFlags(_ string) {}
