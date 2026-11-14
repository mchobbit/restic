//go:build !linux

package fs

// RemoveSELinuxLabel does nothing: SELinux labels are Linux's only.
func RemoveSELinuxLabel(_ string) error {
	return nil
}
