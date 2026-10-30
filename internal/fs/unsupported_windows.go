package fs

// Windows keeps none of the metadata that unsupportedErr is about.
func unsupportedErr(_ error) bool {
	return false
}
