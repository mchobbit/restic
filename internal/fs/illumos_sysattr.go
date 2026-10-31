package fs

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"maps"
	"slices"
	"time"
)

// illumos system attributes (chmod(1)'s S+v..., fgetattr(3C)) as a bitmask,
// the creation time, and the part of libnvpair restic needs to read and
// change them: a packed, XDR-encoded nvlist, which is what each file's
// SUNWattr_rw extended attribute holds.  A read gives every attribute the
// file system has; a write sets just the ones in the nvlist written, so
// restic writes a list of those it changes alone.  Writing back the whole
// view would set the creation time too, which an immutable file refuses.
// Plain Go, so that it is tested on any OS; the file access is in
// flags_illumos.go and birthtime_illumos.go.

// illumosSysattrBits are the attributes SUNWattr_rw holds as booleans, one bit
// each in the mask restic keeps.
var illumosSysattrBits = map[string]uint32{
	"hidden":         1 << 0,
	"system":         1 << 1,
	"readonly":       1 << 2,
	"archive":        1 << 3,
	"nounlink":       1 << 4,
	"immutable":      1 << 5,
	"appendonly":     1 << 6,
	"nodump":         1 << 7,
	"av_quarantined": 1 << 8,
	"av_modified":    1 << 9,
	"offline":        1 << 10,
	"sparse":         1 << 11,
}

// ZFS sets these two on every file it creates or writes to, as Windows does
// its archive bit; on their own they're no reason to keep a file's
// attributes.
const illumosSysattrAutomatic = 1<<3 | 1<<9 // archive, av_modified

const (
	nvEncodeXDR  = 1 // NV_ENCODE_XDR
	nvUniqueName = 1 // NV_UNIQUE_NAME

	dataTypeUint32       = 6  // DATA_TYPE_UINT32
	dataTypeString       = 9  // DATA_TYPE_STRING
	dataTypeUint64Array  = 16 // DATA_TYPE_UINT64_ARRAY
	dataTypeNvlist       = 19 // DATA_TYPE_NVLIST
	dataTypeBooleanValue = 21 // DATA_TYPE_BOOLEAN_VALUE
)

// nvValue is the type and encoded value of a pair in an XDR-encoded nvlist.
type nvValue struct {
	typ, nelem uint32
	value      []byte // up to the end of the pair
}

// nvlistValues returns the pairs at the top of an XDR-encoded nvlist, by
// name.  Nested nvlists are skipped, as pairs of every other type are: each
// pair starts with its encoded size.
func nvlistValues(buf []byte) (map[string]nvValue, error) {
	be := binary.BigEndian
	if len(buf) < 12 || buf[0] != nvEncodeXDR {
		return nil, fmt.Errorf("not an XDR-encoded nvlist")
	}
	// The 4-byte nvs header, then the nvlist's version and flags.
	pos := 12
	values := make(map[string]nvValue)
	for {
		if pos+8 > len(buf) {
			return nil, fmt.Errorf("nvlist truncated at %d", pos)
		}
		start := pos
		encodedSize := int(be.Uint32(buf[pos:]))
		decodedSize := be.Uint32(buf[pos+4:])
		if encodedSize == 0 && decodedSize == 0 {
			return values, nil // the end
		}
		if encodedSize < 8 || start+encodedSize > len(buf) {
			return nil, fmt.Errorf("nvpair at %d has size %d", start, encodedSize)
		}
		end := start + encodedSize
		pos += 8
		nameLen := int(be.Uint32(buf[pos:]))
		pos += 4
		if nameLen < 0 || pos+nameLen > end {
			return nil, fmt.Errorf("nvpair at %d: name too long", start)
		}
		name := string(buf[pos : pos+nameLen])
		pos += (nameLen + 3) &^ 3
		if pos+8 > end {
			return nil, fmt.Errorf("nvpair %q truncated", name)
		}
		values[name] = nvValue{typ: be.Uint32(buf[pos:]), nelem: be.Uint32(buf[pos+4:]), value: buf[pos+8 : end]}
		pos = end
	}
}

// nvPair is a pair to encode in an nvlist.  Its value is a bool
// (DATA_TYPE_BOOLEAN_VALUE), uint32, string, []uint64 or a nested []nvPair.
type nvPair struct {
	name  string
	value any
}

// encodeNvlist packs pairs, in their order, as nvlist_pack(3NVPAIR) does with
// NV_ENCODE_XDR and NV_UNIQUE_NAME.
func encodeNvlist(pairs []nvPair) []byte {
	// The nvs header: the encoding and the host's byte order (little
	// endian; XDR doesn't use it), then two bytes unused.
	return appendNvlist([]byte{nvEncodeXDR, 1, 0, 0}, pairs)
}

func appendNvlist(buf []byte, pairs []nvPair) []byte {
	be := binary.BigEndian
	buf = be.AppendUint32(buf, 0) // nvl_version
	buf = be.AppendUint32(buf, nvUniqueName)
	for _, p := range pairs {
		var typ uint32
		nelem := uint32(1)
		var value []byte
		var native int // the value's size in memory, for the decoded size
		switch v := p.value.(type) {
		case bool:
			typ, native = dataTypeBooleanValue, 4
			var b uint32
			if v {
				b = 1
			}
			value = be.AppendUint32(nil, b)
		case uint32:
			typ, native = dataTypeUint32, 4
			value = be.AppendUint32(nil, v)
		case string:
			typ, native = dataTypeString, len(v)+1
			value = appendXDRString(nil, v)
		case []uint64:
			typ, nelem, native = dataTypeUint64Array, uint32(len(v)), 8*len(v)
			value = be.AppendUint32(nil, nelem)
			for _, x := range v {
				value = be.AppendUint64(value, x)
			}
		case []nvPair:
			typ, native = dataTypeNvlist, 24 // sizeof (nvlist_t)
			value = appendNvlist(nil, v)
		default:
			panic(fmt.Sprintf("nvpair %q: no encoding for %T", p.name, v))
		}
		start := len(buf)
		buf = be.AppendUint32(buf, 0) // the encoded size, below
		// The decoded size: an nvpair_t, then the name and the value, each
		// 8-byte aligned.
		buf = be.AppendUint32(buf, uint32(16+align8(len(p.name)+1)+align8(native)))
		buf = appendXDRString(buf, p.name)
		buf = be.AppendUint32(buf, typ)
		buf = be.AppendUint32(buf, nelem)
		buf = append(buf, value...)
		be.PutUint32(buf[start:], uint32(len(buf)-start))
	}
	// The end of the list: a pair with both sizes 0.
	return be.AppendUint64(buf, 0)
}

// appendXDRString appends s as XDR does: its length, then its bytes padded to
// 4, without a NUL.
func appendXDRString(buf []byte, s string) []byte {
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(s)))
	buf = append(buf, s...)
	return append(buf, make([]byte, (4-len(s)%4)%4)...)
}

func align8(n int) int {
	return (n + 7) &^ 7
}

// sysattrMask reads the system attributes in a SUNWattr_rw view as a mask.
func sysattrMask(view map[string]nvValue) uint32 {
	var mask uint32
	for name, bit := range illumosSysattrBits {
		v, ok := view[name]
		if ok && v.typ == dataTypeBooleanValue && v.nelem == 1 && len(v.value) >= 4 &&
			binary.BigEndian.Uint32(v.value) != 0 {
			mask |= bit
		}
	}
	return mask
}

// sysattrKept returns the mask of the system attributes in a SUNWattr_rw
// view, and whether to keep it: whether it isn't what a new file gets, the
// automatic attributes the view has.  So a mask of 0 is kept where the view
// has them, a file whose archive and av_modified were cleared; a restore
// would set them again.
func sysattrKept(view map[string]nvValue) (uint32, bool) {
	var automatic uint32
	for name, bit := range illumosSysattrBits {
		if v, ok := view[name]; ok && v.typ == dataTypeBooleanValue && bit&illumosSysattrAutomatic != 0 {
			automatic |= bit
		}
	}
	mask := sysattrMask(view)
	return mask, mask != automatic
}

// sysattrMaskNvlist returns the nvlist that sets the system attributes to
// those in mask: a boolean for each attribute the view has, alone.
func sysattrMaskNvlist(view map[string]nvValue, mask uint32) []byte {
	var pairs []nvPair
	for _, name := range slices.SortedFunc(maps.Keys(illumosSysattrBits), func(a, b string) int {
		return cmp.Compare(illumosSysattrBits[a], illumosSysattrBits[b])
	}) {
		if v, ok := view[name]; ok && v.typ == dataTypeBooleanValue {
			pairs = append(pairs, nvPair{name, mask&illumosSysattrBits[name] != 0})
		}
	}
	return encodeNvlist(pairs)
}

// sysattrCrtime returns the creation time in a SUNWattr_rw view, and whether
// it has one: crtime, two uint64s, the seconds and nanoseconds.
func sysattrCrtime(view map[string]nvValue) (time.Time, bool) {
	v, ok := view["crtime"]
	if !ok || v.typ != dataTypeUint64Array || v.nelem != 2 || len(v.value) < 20 ||
		binary.BigEndian.Uint32(v.value) != 2 {
		return time.Time{}, false
	}
	sec := int64(binary.BigEndian.Uint64(v.value[4:]))
	nsec := int64(binary.BigEndian.Uint64(v.value[12:]))
	return time.Unix(sec, nsec), true
}

// crtimeNvlist returns the nvlist that sets the creation time to t, alone.
func crtimeNvlist(t time.Time) []byte {
	return encodeNvlist([]nvPair{{"crtime", []uint64{uint64(t.Unix()), uint64(t.Nanosecond())}}})
}
