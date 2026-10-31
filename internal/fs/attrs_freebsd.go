//go:build freebsd

package fs

import "golang.org/x/sys/unix"

// osACLs is FreeBSD's ACL interface (attrs_bsd.go).
var osACLs = &freebsdACLs

// utimeOmit is UTIME_OMIT (birthtime_bsd.go).
const utimeOmit = unix.UTIME_OMIT
