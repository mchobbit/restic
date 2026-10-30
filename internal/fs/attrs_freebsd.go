//go:build freebsd

package fs

// osACLs is FreeBSD's ACL interface (attrs_bsd.go).
var osACLs = &freebsdACLs
