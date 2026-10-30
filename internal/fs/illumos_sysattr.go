package fs

import (
	"encoding/binary"
	"fmt"
)

// illumos system attributes (chmod(1)'s S+v..., fgetattr(3C)) as a bitmask,
// and the one part of libnvpair restic needs to read and change them: the
// booleans in a packed, XDR-encoded nvlist, which is what each file's
// SUNWattr_rw extended attribute holds.  Plain Go, so that it is tested on
// any OS; the file access is in flags_illumos.go.

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

// ZFS sets these two on every file it writes, as Windows does its archive
// bit; on their own they're no reason to keep a file's attributes.
const illumosSysattrAutomatic = 1<<3 | 1<<9 // archive, av_modified

const (
	nvEncodeXDR          = 1  // NV_ENCODE_XDR
	dataTypeBooleanValue = 21 // DATA_TYPE_BOOLEAN_VALUE
)

// nvlistBooleans returns the offset in buf of the value of each boolean in an
// XDR-encoded nvlist: changing those 4 bytes and writing buf back changes
// just that boolean.
func nvlistBooleans(buf []byte) (map[string]int, error) {
	be := binary.BigEndian
	if len(buf) < 12 || buf[0] != nvEncodeXDR {
		return nil, fmt.Errorf("not an XDR-encoded nvlist")
	}
	// The 4-byte nvs header, then the nvlist's version and flags.
	pos := 12
	values := make(map[string]int)
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
		pos += 8
		nameLen := int(be.Uint32(buf[pos:]))
		pos += 4
		if pos+nameLen > start+encodedSize {
			return nil, fmt.Errorf("nvpair at %d: name too long", start)
		}
		name := string(buf[pos : pos+nameLen])
		pos += (nameLen + 3) &^ 3
		if pos+8 > start+encodedSize {
			return nil, fmt.Errorf("nvpair %q truncated", name)
		}
		typ, nelem := be.Uint32(buf[pos:]), be.Uint32(buf[pos+4:])
		pos += 8
		if typ == dataTypeBooleanValue && nelem == 1 && pos+4 <= start+encodedSize {
			values[name] = pos
		}
		pos = start + encodedSize
	}
}

// sysattrMask reads the system attributes in a SUNWattr_rw view as a mask.
func sysattrMask(view []byte) (uint32, error) {
	offsets, err := nvlistBooleans(view)
	if err != nil {
		return 0, err
	}
	var mask uint32
	for name, bit := range illumosSysattrBits {
		if off, ok := offsets[name]; ok && binary.BigEndian.Uint32(view[off:]) != 0 {
			mask |= bit
		}
	}
	return mask, nil
}

// setSysattrMask sets the system attributes in a SUNWattr_rw view, in place,
// to those in mask.  Attributes the view lacks are left out.
func setSysattrMask(view []byte, mask uint32) error {
	offsets, err := nvlistBooleans(view)
	if err != nil {
		return err
	}
	for name, bit := range illumosSysattrBits {
		if off, ok := offsets[name]; ok {
			var v uint32
			if mask&bit != 0 {
				v = 1
			}
			binary.BigEndian.PutUint32(view[off:], v)
		}
	}
	return nil
}
