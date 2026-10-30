//go:build !linux && !freebsd && !darwin && !illumos && !windows

package fs

// No SEEK_HOLE probe here (OpenBSD, NetBSD, DragonFly, Solaris): a file with
// fewer blocks than its size counts as sparse.  DragonFly has SEEK_HOLE, but
// HAMMER2 answers EOPNOTSUPP and tmpfs always the file's size, which would
// miss real holes.
const seekHole = -1
