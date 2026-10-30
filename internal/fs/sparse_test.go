package fs

import (
	"syscall"
	"testing"

	rtest "github.com/restic/restic/internal/test"
)

func TestHasHoles(t *testing.T) {
	hole := func(off int64, err error) func() (int64, error) {
		return func() (int64, error) { return off, err }
	}
	mustNotSeek := func() (int64, error) {
		t.Fatal("SEEK_HOLE on a file whose blocks leave no room for holes")
		return 0, nil
	}

	for _, test := range []struct {
		name         string
		size, blocks int64
		firstHole    func() (int64, error)
		want         bool
	}{
		{"empty", 0, 0, mustNotSeek, false},
		{"dense", 4096, 8, mustNotSeek, false},
		{"last block part used", 4000, 8, mustNotSeek, false},
		{"preallocated beyond its size", 4096, 64, mustNotSeek, false},
		{"all hole", 1 << 30, 0, hole(0, nil), true},
		{"hole in the middle", 1 << 20, 8, hole(4096, nil), true},
		{"compressed, no holes", 1 << 20, 8, hole(1<<20, nil), false},
		{"SEEK_HOLE fails", 1 << 20, 8, hole(0, syscall.EINVAL), false},
		{"no SEEK_HOLE", 1 << 20, 8, nil, true},
		{"no SEEK_HOLE, dense", 1 << 20, 2048, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rtest.Equals(t, test.want, hasHoles(test.size, test.blocks, test.firstHole))
		})
	}
}
