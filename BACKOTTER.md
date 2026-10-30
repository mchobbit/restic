# restic for BackOtter

This branch (`backotter`) is restic with a few patches that BackOtter's
rescue stick needs, kept on top of restic's latest release. It is not a
separate project: each patch is small, and the branch is rebased onto every
new restic release rather than merged.

## What it adds

- File flags that stock restic doesn't back up, stored as generic attributes
  with OS-prefixed names, so stock restic can still read and restore every
  snapshot (it warns once about the unknown attributes and skips them):
  - `freebsd.file_flags`: FreeBSD's `chflags(1)` flags (`st_flags`).
  - `linux.inode_flags`: the `chattr(1)` flags (`FS_IOC_GETFLAGS`), limited to
    those a user can set.
  Only files that have flags get the attribute, so trees of files without
  flags are byte-for-byte what stock restic writes.
- On restore, flags are set after everything else (contents, hard links,
  ownership, xattrs, timestamps, mode), since an immutable or append-only
  file refuses most of those.  Where the target file system doesn't support a
  flag, the others are still set and restic warns about that one.
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
