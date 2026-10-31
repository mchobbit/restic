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
    set the creation time too, which an immutable file refuses.
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
  others are still set and restic warns about that one (S10 below).  Where it doesn't
  support a POSIX.1e ACL, restic reports that and narrows the group bits to
  the owning group's own access, so a file never comes back more open.
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
- illumos ZFS project IDs are restored with the extended attributes'
  metadata, before the flags, as each node's own ID and inheritance,
  whatever a file got from the directory it was created in; on another file
  system than ZFS (`ENOTTY`) they are a warning (S10).  ZFS on illumos has no
  `FS_IOC_FSGETXATTR`, but ioctls `'X'` 31 and 32 with an 8-byte struct of
  the flags, of which only inheritance (`0x20000000`) is used, and the
  project ID (checked on OmniOS r151058, with `truss` and `zfs project`).
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
- The version reads `X.Y.Z+backotter.N`, so `restic version` and every
  snapshot's program version say which build made it.

## Rules shared with rustic-bko

rustic-bko's BACKOTTER.md states the same rules, in the same words where it
can.  S1 to S11 are their numbers in BackOtter's plan.

- **S1 Access times.**  Backup stores each file's and directory's real atime
  by default; `--with-atime=false` stores the mtime in its place, as stock
  restic does (the `=` is needed: `--with-atime false` backs up a path named
  "false").  rustic-bko's switch is `--set-atime mtime`.  Neither tool
  changes the source's atimes where it can help it: on Linux every file
  opened for reading and every directory opened for listing gets
  `O_NOATIME`, retried without it on `EPERM` (neither its owner nor root).
  Other systems have no `O_NOATIME`, and there both tools update atimes the
  same way.  `rewrite` keeps the stored atimes.  `restore --verify` reads
  the restored files with `O_NOATIME`, so it doesn't undo their atimes.
- **S2 Ownership.**  Restore gives files their numeric uid and gid by
  default.  `--ownership-by-name` (rustic-bko: `--by-name`, or its alias
  `--ownership-by-name`) gives them the IDs their user and group names have
  on the target instead; a name that is empty or unknown there falls back
  to the saved ID, never to 0.
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
     other error (a failed open, `ENXIO`, `EACCES`, ...) means not sparse;
  4. on Linux the probe uses the descriptor opened for the inode flags and
     project ID, so each file is opened once; if that open fails, the file
     is not sparse.  Elsewhere the probe opens the file with
     `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_NOCTTY|O_CLOEXEC`.  The decision always
     uses the lstat's size and blocks.

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
  `ftruncate`, without preallocation, and of each of its chunks the longest
  run of zeros at the start is left as a hole (a chunk of zeros is not
  written at all), the rest written.  Should such a file turn out to have
  data when it is opened (a race), it is truncated to 0 first, so that no
  hole sits over old bytes.
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
  a warning, not an error.
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
  `illumos.group_sid`, `illumos.xattr_meta` and `illumos.zfs_project`.)
- **S10 Unsupported versus failed.**  When the target file system refuses an
  inode flag, file flag, ZFS DOS flag, F or project ID with `ENOTTY`,
  `EOPNOTSUPP`/`ENOTSUP` or `EINVAL` (it lacks them), or isn't ZFS for ZFS
  DOS flags, restic warns once per attribute and refused bits, naming the
  first file, and gives the number of files at the end; the restore still
  exits 0.  On FreeBSD, OpenBSD, NetBSD, DragonFly and illumos the same
  goes for their file flags.  Any
  other failure (`EPERM`, `EDQUOT`, `EIO`, ...) is an error, and restic
  exits 1, while rustic-bko only warns about it and exits 0 (upstream
  rustic's design).  That is the one exit-status difference left between
  the tools.
- **S11 Opening files for metadata.**  Regular files and directories are
  opened with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_NOCTTY|O_CLOEXEC` (and
  `O_DIRECTORY` for directories) and checked with `fstat`: a file of another
  type than its node's gets no ioctls, nor, at backup, one that isn't the
  inode the lstat saw.

## Building

    ./build-backotter.sh [GOOS/GOARCH ...]
    # default: linux/amd64 freebsd/amd64 netbsd/amd64 dragonfly/amd64

Builds without the `selfupdate` tag, so there is no `self-update` command to
replace this build with stock restic.  Binaries go to `dist/`, named
`restic-backotter-VERSION-GOOS-GOARCH`.

## Moving to a new restic release

    git fetch upstream --tags
    git rebase --onto vNEW vOLD backotter
    # set internal/global/global.go's Version to "NEW+backotter.1"
    git push --force-with-lease origin backotter
