package fs

// The file flags kept on NetBSD (flags_netbsd.go) and DragonFly
// (flags_dragonfly.go), as masks of st_flags, and when FreeBSD keeps them
// (flags_freebsd.go).  Plain Go, so that they are tested on any OS.

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

// The flags that keep a file from being written, appended to only, or
// removed and renamed (<sys/stat.h>), the same bits on every BSD that has
// them.
const (
	bsdUFImmutable     = 0x00000002 // "uchg"
	bsdUFAppend        = 0x00000004 // "uappnd"
	bsdUFNounlink      = 0x00000010 // "uunlnk" (FreeBSD, DragonFly)
	freebsdUFReadonly  = 0x00001000 // "urdonly" (ZFS: DOS read-only)
	bsdSFImmutable     = 0x00020000 // "schg"
	bsdSFAppend        = 0x00040000 // "sappnd"
	bsdSFNounlink      = 0x00100000 // "sunlnk" (FreeBSD, DragonFly)
	bsdRestrictingBase = bsdUFImmutable | bsdUFAppend | bsdSFImmutable | bsdSFAppend
)

// The flags a restore clears from a file it finds in its way before it
// changes it (ClearRestrictingFlags), per system: immutable, append-only
// and undeletable, the owner's and the system's, and on FreeBSD ZFS's DOS
// read-only, which refuses writes too.  The late flags pass sets the
// node's.
const (
	freebsdRestrictingFlags   = bsdRestrictingBase | bsdUFNounlink | bsdSFNounlink | freebsdUFReadonly
	netbsdRestrictingFlags    = bsdRestrictingBase
	openbsdRestrictingFlags   = bsdRestrictingBase
	dragonflyRestrictingFlags = bsdRestrictingBase | bsdUFNounlink | bsdSFNounlink
)

// freebsdUFArchive is FreeBSD's UF_ARCHIVE ("uarch"), which ZFS sets on
// every file it creates or writes to, as Windows does its archive bit.
const freebsdUFArchive = 0x00000800

// keptFreeBSDFileFlags tells whether a node keeps flags, a file's kept file
// flags on FreeBSD, zfs telling whether the file is on ZFS.  On ZFS, not
// when they are archive alone, as on every new or written file, so that a
// tree of ordinary files gets no attribute; but when they are none at all,
// since a file restored without the attribute gets archive back from its
// contents' write (as linux.zfs_dos_flags on Linux).  Elsewhere whenever
// there are any, archive alone too, which no file system but ZFS sets by
// itself.
func keptFreeBSDFileFlags(flags uint32, zfs bool) bool {
	if zfs {
		return flags != freebsdUFArchive
	}
	return flags != 0
}
