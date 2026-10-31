package fs

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	rtest "github.com/restic/restic/internal/test"
)

func TestBirthTimeFirst(t *testing.T) {
	mtime := time.Unix(1200000000, 222222222)
	for _, test := range []struct {
		name  string
		birth time.Time // zero: no attribute
		first bool      // set before the final times: two utimensat calls, else one
	}{
		{"earlier", time.Unix(1000000000, 111111111), true},
		{"a nanosecond earlier", mtime.Add(-1), true},
		{"equal", mtime, false},
		{"later", time.Unix(1300000000, 0), false},
		{"none", time.Time{}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := &data.Node{ModTime: mtime}
			node.SetBirthTime(test.birth)
			birth, first, err := birthTimeFirst(node)
			rtest.OK(t, err)
			rtest.Equals(t, test.first, first)
			if first {
				rtest.Assert(t, birth.Equal(test.birth), "birth time %v set as %v", test.birth, birth)
			}
		})
	}

	node := &data.Node{ModTime: mtime, GenericAttributes: map[data.GenericAttributeType]json.RawMessage{
		data.TypeUnixBirthTime: json.RawMessage(`"x"`),
	}}
	_, first, err := birthTimeFirst(node)
	rtest.Assert(t, !first && IsWarning(err), "malformed birth time: want a warning, got %v, %v", first, err)
}
