//go:build !illumos

package fs

// isQuarantined tells whether the regular file at path has illumos'
// av_quarantined system attribute, which only illumos has.
func isQuarantined(_ string) bool {
	return false
}
