//go:build !linux && !darwin

package fs

// FillHoles does nothing here: there is no preallocation to fill holes with
// (PreallocateFile only sets the size).
func FillHoles(_ string, _ int64) {}
