package fs

// The file flags kept on NetBSD (flags_netbsd.go) and DragonFly
// (flags_dragonfly.go), as masks of st_flags.  Plain Go, so that they are
// tested on any OS.

// From NetBSD's <sys/stat.h>: the flags the kernel manages itself.
const (
	netbsdSFSnapshot  = 0x00200000 // SF_SNAPSHOT, an FFS snapshot file
	netbsdSFLog       = 0x00400000 // SF_LOG, the WAPBL journal's inode
	netbsdSFSnapinval = 0x00800000 // SF_SNAPINVAL, an invalid snapshot
)

// netbsdFileFlags are the flags kept on NetBSD: all that an owner (0xffff)
// or root may set, but the kernel's own.  chflags(2) refuses SF_SNAPSHOT and
// SF_SNAPINVAL, and takes SF_LOG, which must not be set on a restored file.
const netbsdFileFlags = 0xffffffff &^ (netbsdSFSnapshot | netbsdSFLog | netbsdSFSnapinval)

// dragonflyFileFlags are the flags kept on DragonFly: all of them.  Root can
// set every bit on HAMMER2 and UFS (an owner 0xffff), and none is the
// kernel's own (SF_UNUSED17 is FreeBSD's SF_SNAPSHOT).
const dragonflyFileFlags = 0xffffffff
