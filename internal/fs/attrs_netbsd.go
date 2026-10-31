//go:build netbsd

package fs

// osACLs is NetBSD's ACL interface (attrs_bsd.go): FreeBSD's, under other
// system call numbers and pathconf(2) names, and on symlinks too.  ACLs and
// extended attributes need FFSv2ea (newfs -O 2ea); on plain FFSv2, such as
// a default install's root, the system calls fail with EOPNOTSUPP, and a
// file simply has none.
var osACLs = &netbsdACLs

// utimeOmit is UTIME_OMIT (birthtime_bsd.go), from <sys/stat.h>: x/sys/unix
// lacks it for NetBSD.
const utimeOmit = 1<<30 - 2
