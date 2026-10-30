//go:build !linux && !freebsd && !darwin && !illumos && !windows

package fs

// No SEEK_HOLE here (OpenBSD, NetBSD, DragonFly in x/sys/unix): a file with
// fewer blocks than its size counts as sparse.
const seekHole = -1
