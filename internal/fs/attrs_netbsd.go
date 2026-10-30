//go:build netbsd

package fs

// osACLs is NetBSD's ACL interface (attrs_bsd.go): FreeBSD's, under other
// system call numbers and pathconf(2) names, and on symlinks too.  ACLs and
// extended attributes need FFSv2ea (newfs -O 2ea); on plain FFSv2, such as
// a default install's root, the system calls fail with EOPNOTSUPP, and a
// file simply has none.
var osACLs = &netbsdACLs
