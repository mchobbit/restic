//go:build illumos

package fs

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// SIDs through idmap(8): a file owned by a SID (a Windows user or group, as
// the SMB server and ZFS keep them) has an ephemeral uid or gid, which idmapd
// hands out as it is asked for and forgets at a reboot.  So restic keeps the
// SID, and a restore asks idmapd for the id the SID has now.  libidmap is
// reached as libc is (attrs_illumos.go): the Go assembler addresses a
// dynamic import as one must be, absolutely, only when its name starts with
// libc_, whatever library it comes from.

//go:cgo_import_dynamic libc_idmap_get_create idmap_get_create "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_sidbyuid idmap_get_sidbyuid "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_sidbygid idmap_get_sidbygid "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_uidbysid idmap_get_uidbysid "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_gidbysid idmap_get_gidbysid "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_mappings idmap_get_mappings "libidmap.so.1"
//go:cgo_import_dynamic libc_idmap_get_destroy idmap_get_destroy "libidmap.so.1"
//go:cgo_import_dynamic libc_free free "libc.so"

//go:linkname procIdmapGetCreate libc_idmap_get_create
//go:linkname procIdmapGetSIDByUID libc_idmap_get_sidbyuid
//go:linkname procIdmapGetSIDByGID libc_idmap_get_sidbygid
//go:linkname procIdmapGetUIDBySID libc_idmap_get_uidbysid
//go:linkname procIdmapGetGIDBySID libc_idmap_get_gidbysid
//go:linkname procIdmapGetMappings libc_idmap_get_mappings
//go:linkname procIdmapGetDestroy libc_idmap_get_destroy
//go:linkname procFree libc_free

var (
	procIdmapGetCreate,
	procIdmapGetSIDByUID,
	procIdmapGetSIDByGID,
	procIdmapGetUIDBySID,
	procIdmapGetGIDBySID,
	procIdmapGetMappings,
	procIdmapGetDestroy,
	procFree uintptr
)

// idmapStat is an idmap_stat other than IDMAP_SUCCESS.  The calls return
// one as an int32, the rest of the register being undefined.
type idmapStat int32

func (s idmapStat) Error() string {
	return fmt.Sprintf("idmap status %d", int32(s))
}

// idmapResult is where the answer to one request goes: libidmap keeps the
// pointers it is given with the request, and fills them in when
// idmap_get_mappings has the answer.
type idmapResult struct {
	stat   int32
	id     uint32 // a uid or gid
	rid    uint32
	prefix *byte // malloc(3)ed by libidmap
}

// idmapRequest makes one request to idmapd: queue adds it to the handle gh
// (idmap_get_sidbyuid, ...) and returns its idmap_stat.
func idmapRequest(queue func(gh uintptr, res *idmapResult) idmapStat) (*idmapResult, error) {
	res := &idmapResult{}
	// res must stay where libidmap was told it is until the answer is in.
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(res)

	var gh uintptr
	r, _, _ := sysvicall6(uintptr(unsafe.Pointer(&procIdmapGetCreate)), 1, uintptr(unsafe.Pointer(&gh)), 0, 0, 0, 0, 0)
	if idmapStat(r) != 0 {
		return res, idmapStat(r)
	}
	defer func() {
		_, _, _ = sysvicall6(uintptr(unsafe.Pointer(&procIdmapGetDestroy)), 1, gh, 0, 0, 0, 0, 0)
	}()
	if stat := queue(gh, res); stat != 0 {
		return res, stat
	}
	r, _, _ = sysvicall6(uintptr(unsafe.Pointer(&procIdmapGetMappings)), 1, gh, 0, 0, 0, 0, 0)
	if idmapStat(r) != 0 {
		return res, idmapStat(r)
	}
	if res.stat != 0 {
		return res, idmapStat(res.stat)
	}
	return res, nil
}

type idKey struct {
	id    uint32
	group bool
}

type sidKey struct {
	sid   string
	group bool
}

// What idmapd answered, which it keeps until a reboot: files owned by a SID
// tend to come in numbers, with the same few SIDs.
var (
	sidsByID sync.Map // idKey -> string
	idsBySID sync.Map // sidKey -> uint32
)

// sidOfID returns the SID that the ephemeral uid id (gid, if group) is for.
func sidOfID(id uint32, group bool) (string, error) {
	key := idKey{id, group}
	if sid, ok := sidsByID.Load(key); ok {
		return sid.(string), nil
	}
	proc := &procIdmapGetSIDByUID
	if group {
		proc = &procIdmapGetSIDByGID
	}
	res, err := idmapRequest(func(gh uintptr, res *idmapResult) idmapStat {
		r, _, _ := sysvicall6(uintptr(unsafe.Pointer(proc)), 6, gh, uintptr(id), 0,
			uintptr(unsafe.Pointer(&res.prefix)), uintptr(unsafe.Pointer(&res.rid)), uintptr(unsafe.Pointer(&res.stat)))
		return idmapStat(r)
	})
	if res.prefix != nil {
		defer func() {
			_, _, _ = sysvicall6(uintptr(unsafe.Pointer(&procFree)), 1, uintptr(unsafe.Pointer(res.prefix)), 0, 0, 0, 0, 0)
		}()
	}
	if err != nil {
		return "", err
	}
	if res.prefix == nil {
		return "", fmt.Errorf("idmap gave no SID for %d", id)
	}
	sid := fmt.Sprintf("%s-%d", unix.BytePtrToString(res.prefix), res.rid)
	sidsByID.Store(key, sid)
	return sid, nil
}

// idOfSID returns the uid (gid, if group) that sid has here, which idmapd
// hands out if it hasn't yet.
func idOfSID(sid string, group bool) (uint32, error) {
	key := sidKey{sid, group}
	if id, ok := idsBySID.Load(key); ok {
		return id.(uint32), nil
	}
	prefix, rid, err := splitSID(sid)
	if err != nil {
		return 0, err
	}
	p, err := unix.BytePtrFromString(prefix)
	if err != nil {
		return 0, err
	}
	proc := &procIdmapGetUIDBySID
	if group {
		proc = &procIdmapGetGIDBySID
	}
	res, err := idmapRequest(func(gh uintptr, res *idmapResult) idmapStat {
		r, _, _ := sysvicall6(uintptr(unsafe.Pointer(proc)), 6, gh, uintptr(unsafe.Pointer(p)), uintptr(rid), 0,
			uintptr(unsafe.Pointer(&res.id)), uintptr(unsafe.Pointer(&res.stat)))
		return idmapStat(r)
	})
	// libidmap copies the prefix, but keep it until the answer is in all
	// the same.
	runtime.KeepAlive(p)
	if err != nil {
		return 0, err
	}
	idsBySID.Store(key, res.id)
	return res.id, nil
}
