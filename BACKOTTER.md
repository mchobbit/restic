# restic for BackOtter

This branch (`backotter`) is restic with a few patches that BackOtter's
rescue stick needs, kept on top of restic's latest release. It is not a
separate project: each patch is small, and the branch is rebased onto every
new restic release rather than merged.

## What it adds

- Metadata that stock restic doesn't back up, stored as generic attributes
  with OS-prefixed names, so stock restic can still read and restore every
  snapshot (it warns once about the unknown attributes and skips them):
  - `freebsd.file_flags`: FreeBSD's `chflags(1)` flags (`st_flags`), all
    but `SF_SNAPSHOT`.  On ZFS (statfs's `f_fstypename` `zfs`), which
    gives every file it creates or writes to `uarch` (`UF_ARCHIVE`, the
    DOS archive flag), only when they aren't `uarch` alone, and then even
    when they are none: a file whose archive flag was cleared keeps that,
    as 0, as with `linux.zfs_dos_flags`.  Elsewhere whenever there are
    any, `uarch` alone too (no other file system sets it by itself), but
    not none.  A restore sets whatever the attribute holds.
  - `linux.inode_flags`: the `chattr(1)` flags (`FS_IOC_GETFLAGS`), limited to
    those a user can set.
  - `freebsd.acl_access`, `freebsd.acl_default`: POSIX.1e ACLs (UFS mounted
    `acls`), access and directories' default.
  - `freebsd.acl_nfs4`: NFSv4 ACLs (ZFS, UFS mounted `nfsv4acls`).
  - `freebsd.system_extattrs`: extended attributes in the system namespace
    (restic itself keeps only the user namespace on FreeBSD).
  - `illumos.acl`: ZFS's ACLs on illumos (NFSv4-style ACEs).  An entry for
    a SID (a user or group with an ephemeral id, above 2147483647, and not
    `owner@`, `group@` or `everyone@`) has the SID too, in `"sid"`, which
    older readers ignore: `{"who":2147483650,"mask":1,"sid":"S-1-5-21-1111-2222-3333-1004"}`.
  - `illumos.owner_sid`, `illumos.group_sid`: the SID a file's owner or
    group is, as a string (`"S-1-5-21-1111-2222-3333-1001"`), when its uid
    or gid is ephemeral.  See "SIDs on illumos" below.
  - `illumos.system_attributes`: illumos' system attributes (`chmod(1)`'s
    `S+v...`), as a mask, read and written through each file's
    `SUNWattr_rw` view, whenever they aren't what ZFS gives a new file
    (archive and av_modified), 0 included: a file whose archive or
    av_modified was cleared keeps that.  A restore writes a packed nvlist
    of the attributes' booleans alone, never the view it read: that would
    set the creation time too, which an immutable file refuses.  Each
    write names every attribute, and one the restore may not change
    refuses the whole write: for an owner who isn't root, setting nodump,
    appendonly, nounlink or immutable, and clearing av_modified, which
    writing the contents set (naming one without changing it needs no
    privilege).  So when the mask can't be written at once, the restore
    changes the attributes that differ one at a time, from what the file
    has, and names those refused: hidden and archive still come back
    beside an immutable a non-root restore can't set, and archive is
    cleared where av_modified can't be.  The view of a file that can't be
    opened (`EACCES`: root too, with av_quarantined) is reached through
    its attribute directory alone, as `ls -/` and `chmod S` reach it
    (libc's `__openattrdirat`, which `getattrat(3C)` uses), at backup and
    restore.
  - `illumos.xattr_meta`: the owner, group, mode, times and ACL of each of
    a file's extended attributes (attribute files, `runat(1)`), by name:
    `{"note":{"uid":1500,"gid":1501,"mode":416,"mtime":"2001-09-09T01:46:40.123456789Z","atime":"2001-09-09T01:46:40Z","acl":[...]}}`,
    the times in the form of the node's, `"acl"` only when the ACL says
    more than the mode, in `illumos.acl`'s form, SIDs included.  Written
    for every file with extended attributes, before their values are read:
    that read moves each attribute's atime (illumos has no `O_NOATIME`), so
    the next backup keeps the time of this one's read, and a file with
    extended attributes gets a new node in each snapshot.
  - `illumos.zfs_project`: a file's ZFS project ID and whether it hands it
    down (`zfs project -p 77 -s`), as `{"id":77,"inherit":true}`
    (`"inherit"` left out when false), when it has either; regular files
    and directories.
  - `freebsd.zfs_project`: the same on FreeBSD's ZFS (`zfs project`;
    regular files and directories on ZFS, by statfs's `f_fstypename`), in
    the same form.  OpenZFS takes Linux's `struct fsxattr` (28 bytes) there,
    with ioctls `'X'` 31 and 32 (`0x401c581f`, `0x801c5820`, as `truss(1)`
    shows `zfs project` calling them on FreeBSD 15.1); only inheritance
    (`0x20000000`) of its flags is used.  Project quotas
    (`projectquota@N`) count by it.
  - `illumos.unread_size`: on a regular file whose contents weren't backed
    up, because av_quarantined kept it closed (below), the size it had
    then, in bytes, as a number (`1234`); the node itself has size 0 and no
    content.
  - `openbsd.file_flags`: OpenBSD's `chflags(1)` flags.
  - `netbsd.file_flags`: NetBSD's `chflags(1)` flags, but those the kernel
    manages (`SF_SNAPSHOT`, `SF_SNAPINVAL`, and `SF_LOG`, which marks the
    log's inode: `chflags(2)` takes it, so it must not come back).
  - `netbsd.acl_access`, `netbsd.acl_default`, `netbsd.acl_nfs4`,
    `netbsd.system_extattrs`: as the FreeBSD ones, in the same form, read
    and set by the same code (NetBSD has FreeBSD's ACL interface under
    other system call numbers and `pathconf` names), and on symlinks too.
    They exist only on FFSv2ea (`newfs -O 2ea`, mounted `posix1eacls` or
    `nfs4acls`); plain FFSv2 has neither ACLs nor extended attributes.
  - `dragonfly.file_flags`: DragonFly's `chflags(1)` flags, all of them
    (`noscache`, `nohistory` and the rest; DragonFly has no ACLs or
    extended attributes).
  - `linux.project_id`: a file's project ID (`chattr -p`), when it isn't 0.
    bcachefs (Linux 6.12) gives in `FS_IOC_FSGETXATTR` the ID a file had
    when its inode was read in, 0 for one set since (`chattr -p`, a
    restore) until it is read in again, and the ID it has in the extended
    attribute `bcachefs_effective.project`: on bcachefs (statfs's `f_type`
    `0xca451a4e`) that is read instead, where there is one, at backup and
    by a restore to see whether the file has the ID already, as in
    rustic-bko.  The restore sets it with `FS_IOC_FSSETXATTR`, as anywhere
    (setting the attribute does nothing); the attribute itself isn't kept
    (below).  On bcachefs a directory gets its project ID, with its own
    options, as soon as the restore creates it (S6), and its entries
    inherit it then; the restore gives no file an ID it has already, so an
    entry that inherited its ID in the original comes back inheriting it,
    with no `bcachefs.project` of its own, and there a node without
    `linux.project_id` gets 0 where the file inherited another.  Elsewhere
    a node without one leaves the file's ID as it is.  Accepted difference
    (both tools): a bcachefs file given its directory's project ID by hand
    comes back inheriting it.
  - `linux.zfs_dos_flags`: the DOS flags OpenZFS keeps on Linux
    (`ZFS_IOC_GETDOSFLAGS`), as a number: readonly, hidden, system,
    archive, nounlink, offline and sparse (mask `0x302f00000000`).
    Immutable, append-only and nodump are in `linux.inode_flags` already;
    reparse can't be set on Linux.  Only on ZFS (statfs's `f_type`
    `0x2fc12fc1`; the ioctl numbers may mean something else to another
    file system), for regular files and directories, and only when they
    aren't archive alone, which ZFS gives every file it creates or writes
    to; archive cleared is kept, as 0.  FreeBSD (`freebsd.file_flags`) and
    illumos (`illumos.system_attributes`) hold the same flags already.
  - `unix.sparse`: `true` on a regular file that had holes.
  - `unix.birthtime`: a file's birth (creation) time, on FreeBSD and NetBSD
    (`st_birthtim`, every type of file) and illumos (ZFS's `crtime`,
    regular files and directories), as a string in the form of the node's
    mtime: RFC 3339 in UTC with every nanosecond, such as
    `"2001-09-09T01:46:40.123456789Z"` or `"2001-09-09T01:46:40Z"`; any
    offset is read.  Not when the file system reports second 0 or -1 (UFS1,
    FFSv1).  Not on Linux, which can't set one, nor OpenBSD or DragonFly,
    which keep none.
  A file only gets an attribute when it has something the mode doesn't say
  (flags, a non-trivial ACL, a project ID, holes), so trees of ordinary
  files are what stock restic writes, except that atimes are real (see
  below), files with holes carry `unix.sparse`, and on FreeBSD, NetBSD and
  illumos every file its birth time; on other systems, with
  `--with-atime=false` and no holes, they are byte-for-byte.
- On restore, ACLs are set after the mode, which rewrites NFSv4 ACLs, and
  flags after everything else (contents, hard links, ownership, xattrs,
  timestamps, mode, ACLs), once per hard-linked file, since an immutable or
  append-only file refuses most of those.  ZFS DOS flags go on just before
  the inode flags: a readonly file refuses writes, a nounlink one renames,
  and any write sets archive again.  `ZFS_IOC_SETDOSFLAGS` replaces the
  whole mask, so restic reads the file's flags and changes only the kept
  ones, leaving immutable, append-only and nodump to the inode flags; an
  owner may set every kept one.  Restored onto another file system than
  ZFS, they get one warning per restore, with the number of files.
  btrfs's no-copy-on-write flag goes on while a new file is still empty:
  btrfs ignores it on one with data.  Where the target file system doesn't support a flag, the
  others are still set and restic warns about that one (S10 below).  Where
  it doesn't support extended attributes (NetBSD's FFSv1 and plain FFSv2),
  or an attribute's namespace (`ENOTSUP`, `EOPNOTSUPP`), restic warns once
  per file, naming each attribute not set, those of FreeBSD's and NetBSD's
  system namespace (`system.NAME`) in a warning of their own, and the
  restore still exits 0; stock restic says nothing.  Where it doesn't
  support ACLs (`EOPNOTSUPP` on FreeBSD and NetBSD), restic warns once per
  file, naming each ACL not set, and the restore still exits 0, as in
  rustic-bko; without a POSIX.1e ACL, it narrows the group bits to the
  owning group's own access, so a file never comes back more open.
- Birth times are restored just before the atime and mtime.  illumos sets
  any `crtime` through the file's `SUNWattr_rw` view, with a packed nvlist
  of `crtime` alone, before the system attributes (an immutable file
  refuses it).  FreeBSD and NetBSD have no call that sets one, but their
  kernels lower the birth time to any mtime set earlier than it (UFS2,
  FFSv2, ZFS; files, directories and symlinks; owners too).  So a birth
  time earlier than the mtime is set as the mtime first, with
  `utimensat(2)` and the atime omitted, then the real times, which leave
  it; one that isn't earlier comes back as the mtime, the closest those
  systems can get.  Elsewhere the attribute is skipped quietly.  Failures
  are reported as the times' are.
- A restored symlink keeps its own times on every Unix that can set them
  (`utimensat(2)` with `AT_SYMLINK_NOFOLLOW`, as on Linux), and on FreeBSD,
  OpenBSD, NetBSD and DragonFly its permissions (`fchmodat(2)` with
  `AT_SYMLINK_NOFOLLOW`; a file system that refuses that is left alone);
  stock restic gives it the time of the restore and 0755.
- On illumos, extended attributes (named attribute files, `runat(1)`) are
  backed up and restored, through `openat(2)` with `O_XATTR`: stock restic's
  calls for them fail there with ENOTSUP, which it ignores.  The system
  attribute views `SUNWattr_ro` and `SUNWattr_rw` are left out.  A restore
  creates an attribute file with mode 0600, so that it is never readable by
  others before it gets its own owner, mode, ACL and times
  (`illumos.xattr_meta`), with `fchownat(2)`, `fchmodat(2)`, `facl(2)` and
  `utimensat(2)` on the attribute directory, in that order, once the values
  are written; an attribute the restore left out is left alone.  This goes
  before the file's own times (writing an attribute file leaves them) and
  flags.  A snapshot without the attribute restores as before.
- A file with illumos' `av_quarantined` system attribute can't be opened
  for reading, by root either (`EACCES`), nor can its attribute directory
  through it; stat, the system attributes (above) and the ACL still can.
  So when opening a regular file for its contents fails with `EACCES` and
  its system attributes have av_quarantined, the backup keeps its node
  without them: mode, owner, times, links, inode, device, SID owners, ACL,
  `illumos.system_attributes` (av_quarantined set) and creation time as
  for any file, no content, size 0, and `illumos.unread_size` with its
  real size.  Its extended attributes and `illumos.xattr_meta` can't be
  read (`EACCES`) and are left out, and so is its ZFS project (read through
  a descriptor of the file).  The backup warns once for the file,
  "PATH: av_quarantined: contents not backed up (N bytes)", and still
  exits 0 (an `fs.BackupWarning`); the attribute is never touched.  Any
  other failure to open a file is an error as before.  A file whose node
  has `illumos.unread_size` is never taken as unchanged from a parent
  snapshot: the next backup tries its contents again.  A restore creates
  such a file empty, a new one in place of any file there that isn't empty
  already (that may be quarantined, and can't be read or have its extended
  attributes listed), restores everything else as for any file, the system
  attributes last as always, so that it ends quarantined again, and warns
  once for it, "PATH: contents were not backed up (av_quarantined at
  backup, N bytes)"; that is not an error.  On another OS it is the same
  empty file and warning.  Older restic and stock restic warn once about
  the unknown attribute and restore the empty file.
- illumos ZFS project IDs are restored with the extended attributes'
  metadata, before the flags, as each node's own ID and inheritance,
  whatever a file got from the directory it was created in; on another file
  system than ZFS (`ENOTTY`) they are a warning (S10).  ZFS on illumos has no
  `FS_IOC_FSGETXATTR`, but ioctls `'X'` 31 and 32 with an 8-byte struct of
  the flags, of which only inheritance (`0x20000000`) is used, and the
  project ID (checked on OmniOS r151058, with `truss` and `zfs project`).
- FreeBSD ZFS project IDs (`freebsd.zfs_project`) are restored with the
  system extended attributes, before the flags, as each node's own ID and
  inheritance, as on illumos; on UFS (`ENOTTY`) they are a warning (S10).
- **Restoring over flags that refuse it.**  Before the restore touches a
  file or directory it finds at a node's path (to rewrite it, give it its
  metadata, replace it, link over it, put entries in it, or remove it with
  `--delete`), it clears the flags that would refuse that, keeping the
  others: on FreeBSD immutable, append-only and undeletable, the owner's and
  the system's (`uchg`, `uappnd`, `uunlnk`, `schg`, `sappnd`, `sunlnk`), and
  ZFS's DOS read-only (`urdonly`), which refuses writes too; on NetBSD and
  OpenBSD `uchg`, `uappnd`, `schg`, `sappnd`; on DragonFly those and
  `uunlnk`, `sunlnk`; on Linux immutable and append-only (`i`, `a`), through
  `FS_IOC_FSSETXATTR` where the file system has it (it leaves btrfs's
  `btrfs.compression` alone), else `FS_IOC_SETFLAGS`.  The late flag pass
  then gives every file its node's flags, so a restore over an installed
  FreeBSD (whose `libc.so.7`, `ld-elf.so.1`, `init` and `su` are `schg`)
  puts back the snapshot's contents and metadata.  Files the restore leaves
  alone (`--overwrite never`, or `if-newer` with a newer file) keep their
  flags.  Clearing a system flag takes root at securelevel 0 or below (a
  rescue system's is -1); where it is refused, what the restore does next
  fails as before, with the error.  Before this, such a file kept its
  changed contents and the restore exited 1 ("operation not permitted").
  illumos' immutable, appendonly and nounlink system attributes are not
  cleared yet: a restore over such a file there still fails for it, in both
  tools.  rustic-bko does the same.
- **SIDs on illumos.**  A file owned by a SID (a Windows user or group, as
  the SMB server and ZFS keep them) has an ephemeral uid or gid, which
  idmapd hands out when asked and forgets at a reboot: restored as a number
  after a reboot, it becomes another SID's or nobody (60001).  So backup asks
  libidmap for the SID of every ephemeral owner, group and ACL entry and
  keeps it, and restore asks it for the id the SID has now, and gives the
  file that (a second `lchown(2)`, after the one with the numbers) and the
  ACL entry that.  A SID idmap can't map gets nobody (60001), never the
  number the backup saw, which may be another SID's by now, with a warning
  once per attribute and the number of files at the end; that is not an
  error.  A backup where idmap gives no SID warns once
  (`Warning: <file>: idmap offline: SID owners not kept: ...`), keeps the
  numbers alone, and still succeeds (exit 0), as rustic-bko does; with
  idmapd not running at all, `stat(2)` already gives every ephemeral id
  as 60001, and nothing can be kept.  So **svc:/system/idmap must be
  online** at backup and at restore: BackOtter's illumos rescue system
  should run `svcadm enable -s idmap`.
- **libidmap on illumos.**  restic is built without cgo; it calls libidmap
  as it calls libc, through `//go:cgo_import_dynamic` and the runtime's
  `sysvicall6`.  So the illumos binary links `libidmap.so.1` at load time:
  the run-time linker loads it when restic starts, and restic won't start
  without it.  That is no limit in practice: every illumos system has it
  in `/usr/lib` (`/usr/lib/64` for 64-bit), as part of its core
  (`system/library`), whether idmapd runs or not.  (rustic-bko opens it
  with `dlopen(3)` when first needed instead.)  The imports' names start
  with `libc_` whatever their library: only for those does the Go
  assembler address them absolutely, as dynamic imports must be on
  illumos (`useAbs` in `cmd/internal/obj/x86`).
- Restoring `ID:subfolder` gives the target directory the subfolder's own
  metadata, as it does everything below it; stock restic leaves the target
  as it was, so a restored file system's root keeps mkfs's mode and has no
  SELinux label.
- Access times, holes, Linux project IDs and casefolding follow the rules
  below, which rustic-bko follows too, so that either tool restores the
  other's snapshots the same way.
- Extended attributes are stored in the byte order of their names, on
  every system restic reads them on but Windows (Linux, macOS, FreeBSD,
  NetBSD, illumos), as rustic-bko stores them, so that the two tools'
  nodes are the same; stock restic keeps the order the file system lists
  them in.  The order means nothing to a
  restore, which sets each attribute by name.
- bcachefs' `bcachefs_effective.*` extended attributes are neither backed
  up nor restored (nor removed from a restored file), as in rustic-bko:
  they show the options a file has, its own (`bcachefs.*`, which are kept)
  or inherited from its directory, and its project ID (`linux.project_id`,
  read from `bcachefs_effective.project`), and setting one succeeds but
  does nothing.  Nor is `bcachefs.project`, which bcachefs 1.39 (not Linux
  6.12's) lists for a file given a project ID rather than inheriting it:
  `linux.project_id` carries the ID, and bcachefs refuses to have the
  attribute set to anything but 0 (`EINVAL`), so a restore of a snapshot
  that holds it (from an earlier build) skips it.  Stock restic keeps them
  all.
- Times in nodes (and `unix.birthtime`, `illumos.xattr_meta`) are written
  in UTC with `Z`, as stock restic writes them; stock rustic writes the
  same instants with `+00:00`.  Both tools read either form (any offset),
  so the two differ only in a snapshot's bytes, never in what a restore
  sets.
- The version reads `X.Y.Z+backotter.N`, so `restic version` and every
  snapshot's program version say which build made it.

## Rules shared with rustic-bko

rustic-bko's BACKOTTER.md states the same rules, in the same words where it
can.  S1 to S11 are their numbers in BackOtter's plan; S12 is BackOtter's
own addition, made after that plan with the sparse-file work (batch 3).

- **S1 Access times.**  Backup stores each file's and directory's real atime
  by default; `--with-atime=false` stores the mtime in its place, as stock
  restic does (the `=` is needed: `--with-atime false` backs up a path named
  "false").  rustic-bko's switch is `--set-atime mtime`.  Neither tool
  changes the source's atimes where it can help it: on Linux every file
  opened for reading and every directory opened for listing gets
  `O_NOATIME`, retried without it on `EPERM` (neither its owner nor root).
  Symlinks can't be spared: Linux's `readlink(2)` updates a symlink's atime
  as any read does (always on `strictatime`), and has no `O_NOATIME`, so a
  backup with either tool moves the atimes of the source's symlinks.  The
  snapshot still has the old ones (the `lstat` comes before the
  `readlink`), and a restore gives them back.
  Other systems have no `O_NOATIME`, and there listing a directory moves its
  atime (where the file system keeps them: not read-only, `noatime` or
  ZFS's `atime=off`; reading a file moves its own).  So both tools take a
  directory's `lstat` before they list it, the backup's root included
  (restic's archiver always did; rustic-bko's walker now keeps the metadata
  it takes before it reads a directory), and neither runs its size scan
  there (as with `--no-scan`; restic's scanner and rustic-bko's run beside
  the backup, listing directories before the backup reaches them, which
  gave the snapshot the scan's time as their atimes).  Progress then shows
  no total or ETA.  Files' atimes are those before the backup read them.
  Linux, with `O_NOATIME`, keeps its scan.  `rewrite` keeps the stored
  atimes.  `restore --verify` reads
  the restored files with `O_NOATIME`, so it doesn't undo their atimes.
- **S2 Ownership.**  Restore gives files their numeric uid and gid by
  default.  `--ownership-by-name` (rustic-bko: `--by-name`, or its alias
  `--ownership-by-name`) gives them the IDs their user and group names have
  on the target instead; a name that is empty or unknown there falls back
  to the saved ID, never to 0.  A restore not running as root ignores the
  ownership it can't set, as stock restic does (rustic-bko warns about each
  file); any other metadata it isn't permitted to set (`EPERM`, `EACCES`:
  `trusted.*` extended attributes, FreeBSD's system extended attributes,
  a project ID, ...) gets one warning per file, naming what wasn't set,
  and isn't an error.  The late file and inode flags stay errors (S10).
- **S3 Which files are sparse.**  A regular file whose lstat gives `size`
  and `blocks`:
  1. is not sparse if `size <= 0` or `blocks*512 >= size` (this covers
     preallocated files and swap files);
  2. is sparse if the OS has no SEEK_HOLE probe: the blocks decide.  Both
     tools probe on Linux (and Android), FreeBSD, macOS and illumos, and not
     on OpenBSD, NetBSD, DragonFly, Solaris or anything else.  DragonFly has
     SEEK_HOLE, but HAMMER2 answers `EOPNOTSUPP`, and tmpfs always the file's
     size, which would miss real holes;
  3. otherwise is sparse if `lseek(fd, 0, SEEK_HOLE)` finds a hole before
     `size`.  If the probe fails with `EINVAL`, `ENOTSUP`/`EOPNOTSUPP` or
     `ENOTTY` (the file system can't answer), the blocks decide: sparse.  Any
     other error (a failed open, `ENXIO`, `EACCES`, ...) means not sparse.
     On Linux's JFS and nilfs2 (`statfs` `f_type` `0x3153464a` and
     `0x3434`), whose SEEK_HOLE is the kernel's generic one and always
     answers the size, the probe isn't made and the blocks decide too:
     neither compresses nor keeps data in the inode, so fewer blocks than the
     size mean holes;
  4. on Linux the probe uses the descriptor opened for the inode flags and
     project ID, so each file is opened once; if that open fails, the file
     is not sparse.  Elsewhere the probe opens the file with
     `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_NOCTTY|O_CLOEXEC`.  The decision always
     uses the lstat's size and blocks.

  On DragonFly's HAMMER2 the blocks mark more than files with holes, in
  both tools alike: a file of up to 512 bytes, kept in its inode (0
  blocks), a compressed one (LZ4 by default) and runs of zeros, which
  HAMMER2 doesn't store.  That does no harm: such a file is restored sparse
  (S4), which writes every byte of it but the parts of each chunk that are
  zeros within a block, so it comes back the same, only with holes where
  those zeros were (HAMMER2 itself stores no zeros anyway).

  A sparse file gets `"unix.sparse": true`.  On restore, `false`, no
  attribute or a malformed one (S9) means "not marked".

  On ZFS, SEEK_HOLE waits for a file with unwritten changes to be synced
  (a transaction group, up to a few seconds) before answering.  Both tools
  probe the same files, so both pay that for files being written during
  the backup; skipping such files, or taking the mark from the parent
  snapshot, would make the result depend on timing.
- **S4 Sparse restore.**  A file is restored sparse if and only if it is
  marked `unix.sparse` or `--sparse` was given (rustic-bko: `--sparse
  by-content`; neither tool has a way to force a marked file dense), and it
  goes into a new, empty file: nothing was at its target path when the
  restore queued it, or something other than a regular file, which restic
  always replaces with a new file.  A file restored sparse is sized with
  `ftruncate`, without preallocation, and then each of its chunks is cut
  where the blocks of B bytes, aligned to the file offset, begin: each
  piece of the chunk within one block that is all zeros is left unwritten,
  and every other piece is written as it is, never anything outside the
  chunk (the rest of such a block is another chunk's).  The file is new
  and sized, so bytes never written read as zeros: a block is allocated
  only if some chunk puts a byte other than zero in it.  B is the target
  file system's block size (`fstatfs`'s or `fstatvfs`'s `f_bsize`: 4096 on
  ext4, XFS and tmpfs, the record size on ZFS on Linux and illumos), else
  the file's `st_blksize`, and at least 4096; 4096 when neither is known
  (and on Windows).  A file restored sparse so takes no more blocks than
  the source's when the source's holes are aligned to B, but for the file
  system's own (extent trees and the like).  Should such a file turn out
  to have data when it is opened (a race), it is truncated to 0 first, so
  that no hole sits over old bytes.  Before this rule, only each chunk's
  longest run of zeros at the start was left as a hole; a first version of
  it wrote the partial blocks at each chunk's ends, zeros or not.
- **S5 Dense restore.**  Every other file is fully allocated.  A new file is
  preallocated to its full length when it is first sized, where the OS can
  (Linux `fallocate(fd, 0, 0, size)`, macOS `F_PREALLOCATE`; elsewhere
  nothing), and then every byte, zero chunks included, is written.  An
  existing file that is written into is truncated if it is longer, then
  preallocated the same way.  An existing file left as it is, because its
  contents are right, is preallocated on Linux and macOS if it is not
  restored sparse by S4's first condition and has holes (`blocks*512 <
  size`): that fills the holes of a swap file left by an interrupted
  restore.  Not with `--dry-run`.  Preallocation errors are only logged.
- **S6 Casefolding (`FS_CASEFOLD_FL`, chattr's F) is set at creation.**  On
  Linux, every directory the restore creates, and every existing one that is
  still empty when the restore reaches it, gets its F made to match its
  node's before anything goes in: set when the node has it, cleared (an F
  inherited from the parent) when it doesn't.  That takes one open and
  `FS_IOC_GETFLAGS`, and `FS_IOC_SETFLAGS` only when F differs; a file system
  without inode flags or casefolding is left alone, silently.  This covers
  the target of `ID:subfolder` (its node is the subfolder's) and the
  unselected parents of `--include`d entries, which are created with their
  nodes' F.  A whole-snapshot restore's target, which has no node, is left
  as it is.  The late flag pass (S7) never touches F: if a directory's F
  still differs from its node's at the end (it had entries, or the file
  system can't casefold), restic warns "casefolding (F) not restored on
  PATH: directory not empty or not supported", once per directory.  That is
  a warning, not an error.  On bcachefs the same directories (those created,
  or found empty) get their own options then too, before anything goes in:
  their `bcachefs.*` extended attributes (those `--include-xattr` and
  `--exclude-xattr` select) and their project ID, 0 where the node has
  none.  bcachefs passes a directory's options only to entries created
  after they are set, so set at the end with the other metadata the
  entries would lose the compression, replicas, targets or nocow they
  inherited, and get their project ID as their own.  Errors there are
  silent: the directory's metadata at the end sets them all again, and
  reports them.  As rustic-bko does.
- **S7 Late inode flags on Linux.**  After everything else, each file's kept
  flags but F are set to its node's, keeping the file system's own flags and
  F as they are; when that changes nothing, there is no `FS_IOC_SETFLAGS`
  call at all.  On btrfs every `FS_IOC_SETFLAGS` rewrites the
  `btrfs.compression` property (zstd and lzo become zlib, and "no" goes), so
  for a file with a `btrfs.compression` extended attribute that the restore
  sets: the flags but immutable and append-only are set first, then the
  attribute again, then immutable and append-only through
  `FS_IOC_FSSETXATTR`, which leaves the property alone (checked on Linux
  6.12).
- **S8 Project IDs (`linux.project_id`).**  Backup reads the project ID
  through the descriptor opened for the flags, and stores it only when it
  isn't 0.  Restore sets it before the inode flags (an immutable file refuses
  a new one), and not when it is equal already; an explicit 0 does nothing.
  Symlinks, FIFOs, sockets and devices get no project ID either way: they
  can't be opened for `FS_IOC_FSGETXATTR` without following them or side
  effects (Linux 6.17's `file_getattr(2)` could, but is too new to rely on).
- **S9 Malformed values.**  A value of `linux.inode_flags`,
  `linux.project_id`, `linux.zfs_dos_flags`, `unix.sparse`,
  `unix.birthtime` or a `*.file_flags` attribute that can't be decoded gets
  one warning per attribute per restore, and is ignored.  It is not an
  error.  (restic does the same for `illumos.owner_sid`,
  `illumos.group_sid`, `illumos.xattr_meta`, `illumos.zfs_project`,
  `freebsd.zfs_project` and `illumos.unread_size`.)
- **S10 Unsupported versus failed.**  When the target file system refuses an
  inode flag, file flag, ZFS DOS flag, F or project ID with `ENOTTY`,
  `EOPNOTSUPP`/`ENOTSUP` or `EINVAL` (it lacks them), or isn't ZFS for ZFS
  DOS flags, restic warns once per attribute and refused bits, naming the
  first file, and gives the number of files at the end; the restore still
  exits 0.  On FreeBSD, OpenBSD, NetBSD, DragonFly and illumos the same
  goes for their file flags.  Any
  other failure (`EPERM`, `EDQUOT`, `EIO`, ...; but see S2 for a restore not
  running as root) is an error, and restic
  exits 1, while rustic-bko only warns about it and exits 0 (upstream
  rustic's design).  That is the one exit-status difference left between
  the tools.  A file whose contents can't be written (one that can't be
  opened for writing) is an error in both: restic exits 1, and rustic-bko,
  which panicked there, reports each such file once, restores everything
  else, and exits 1.
- **S11 Opening files for metadata.**  Regular files and directories are
  opened with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_NOCTTY|O_CLOEXEC` (and
  `O_DIRECTORY` for directories) and checked with `fstat`: a file of another
  type than its node's gets no ioctls, nor, at backup, one that isn't the
  inode the lstat saw.
- **S12 Backup doesn't read holes.**  A regular file marked sparse (S3)
  on a system with the SEEK_HOLE probe (Linux and Android, FreeBSD, macOS,
  illumos) is read by its data extents: from the current offset,
  `lseek(SEEK_DATA)` finds the next data and `lseek(SEEK_HOLE)` its end,
  only the data is read (`pread(2)`), and the chunker is given zeros for
  the holes without reading them, never past the size `fstat` gave before
  `SEEK_DATA`.  `ENXIO` from `SEEK_DATA` means a hole up to the end of file
  (the size `fstat` gives before `SEEK_DATA`, or after it if smaller, so
  that data appended meanwhile isn't taken for a hole) only if `SEEK_HOLE`
  finds the hole where the reader is (macOS answers `ENXIO` to both at
  every offset of a file its file system compresses); else the reader is at
  the end, or the rest is read plainly.  Any other `lseek` error,
  `SEEK_HOLE` at or before where the reader is, and every file elsewhere,
  is read plainly from there on, up to whatever end of file a read finds.
  On JFS and nilfs2 (S3), SEEK_HOLE gives the size, so a file there is
  read whole, holes and all.
  The bytes the chunker gets are exactly those plain reading gives, so
  chunks, blob IDs, trees and snapshots are bit-for-bit what they were
  before this rule.  A file that grows or shrinks while it is read gives
  what plain reading would: a hole ends at the size `fstat` gives for each
  buffer of its zeros, and from a size the file shrank below it is read
  plainly, to its end of file.  A hole already found gives zeros even if
  data was written there since (as if it had been read before the write):
  no error, no torn chunk.  Each all-zero chunk (holes give many, all of
  the chunker's minimum size), of any file, is hashed once per file and
  length: the same bytes give the same ID.  The rolling hash isn't skipped
  over long zero runs: all-zero data already gives a boundary at the
  chunker's minimum size, with no rolling over its bytes.  Should a file
  system misreport its holes (`SEEK_DATA` calling data a hole), the
  snapshot would have zeros there, as `cp` and every other hole-aware
  reader would.

## Building

    ./build-backotter.sh [GOOS/GOARCH ...]
    # default: linux/amd64 freebsd/amd64 netbsd/amd64 dragonfly/amd64

Builds without the `selfupdate` tag, so there is no `self-update` command to
replace this build with stock restic.  Binaries go to `dist/`, named
`restic-backotter-VERSION-GOOS-GOARCH`, and `dist/restic-commit` says what
they were built from: the commit (`RESTIC_COMMIT` names it for a copy of the
tree without `.git`, and a tree with changes says so), a digest of the Go
sources (every `.go` file, `go.mod` and `go.sum`, by path and content), Go's
version, and when and where.  The digest tells a build made from a stale
copy of the tree, whatever `RESTIC_COMMIT` says:

    ./build-backotter.sh --source-digest    # in the checkout; needs no Go

## Moving to a new restic release

    git fetch upstream --tags
    git rebase --onto vNEW vOLD backotter
    # set internal/global/global.go's Version to "NEW+backotter.1"
    git push --force-with-lease origin backotter
