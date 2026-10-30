# restic for BackOtter

This branch (`backotter`) is restic with a few patches that BackOtter's
rescue stick needs, kept on top of restic's latest release. It is not a
separate project: each patch is small, and the branch is rebased onto every
new restic release rather than merged.

## What it adds

- Metadata that stock restic doesn't back up, stored as generic attributes
  with OS-prefixed names, so stock restic can still read and restore every
  snapshot (it warns once about the unknown attributes and skips them):
  - `freebsd.file_flags`: FreeBSD's `chflags(1)` flags (`st_flags`).
  - `linux.inode_flags`: the `chattr(1)` flags (`FS_IOC_GETFLAGS`), limited to
    those a user can set.
  - `freebsd.acl_access`, `freebsd.acl_default`: POSIX.1e ACLs (UFS mounted
    `acls`), access and directories' default.
  - `freebsd.acl_nfs4`: NFSv4 ACLs (ZFS, UFS mounted `nfsv4acls`).
  - `freebsd.system_extattrs`: extended attributes in the system namespace
    (restic itself keeps only the user namespace on FreeBSD).
  A file only gets an attribute when it has something the mode doesn't say
  (flags, a non-trivial ACL), so trees of ordinary files are byte-for-byte
  what stock restic writes.
- On restore, ACLs are set after the mode, which rewrites NFSv4 ACLs, and
  flags after everything else (contents, hard links, ownership, xattrs,
  timestamps, mode, ACLs), since an immutable or append-only file refuses
  most of those.  Where the target file system doesn't support a flag, the
  others are still set and restic reports that one.  Where it doesn't
  support a POSIX.1e ACL, restic reports that and narrows the group bits to
  the owning group's own access, so a file never comes back more open.
- On FreeBSD a restored symlink keeps its own times (`utimensat(2)` with
  `AT_SYMLINK_NOFOLLOW`, as on Linux) and permissions (`lchmod(2)`); stock
  restic gives it the time of the restore and 0755.
- Restoring `ID:subfolder` gives the target directory the subfolder's own
  metadata, as it does everything below it; stock restic leaves the target
  as it was, so a restored file system's root keeps mkfs's mode and has no
  SELinux label.
- The version reads `X.Y.Z+backotter.N`, so `restic version` and every
  snapshot's program version say which build made it.

## Building

    ./build-backotter.sh [GOOS/GOARCH ...]     # default: linux/amd64 freebsd/amd64

Builds without the `selfupdate` tag, so there is no `self-update` command to
replace this build with stock restic.  Binaries go to `dist/`, named
`restic-backotter-VERSION-GOOS-GOARCH`.

## Moving to a new restic release

    git fetch upstream --tags
    git rebase --onto vNEW vOLD backotter
    # set internal/global/global.go's Version to "NEW+backotter.1"
    git push --force-with-lease origin backotter
