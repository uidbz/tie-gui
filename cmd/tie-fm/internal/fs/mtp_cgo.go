//go:build linux && cgo

// Package-local MTP backend: browses and transfers files on USB-attached media
// devices (phones, cameras) over libmtp, with no FUSE mount. Exposed under the
// "mtp:" scheme.
//
// Path model: "mtp:/<devKey>" lists a device's storages; "mtp:/<devKey>/<storageID>/<dir>/…"
// lists a folder. A device's object tree is addressed by libmtp object IDs;
// MTPFS resolves path segments to IDs by walking (cached per device).
//
// Concurrency: libmtp device handles are not thread-safe and the ops goroutine
// and UI reload goroutine both reach in, so a single manager mutex serializes
// every libmtp call.
package fs

/*
#cgo pkg-config: libmtp
#include <stdlib.h>
#include <libmtp.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var mtpInitOnce sync.Once

// MTPDevice is a connected media device as shown in the sidebar.
type MTPDevice struct {
	Key   string // stable while connected: "bus-devnum"
	Label string // friendly name, else vendor/product
}

// openDevice holds a live libmtp handle plus a path→folder-id cache.
type openDevice struct {
	handle    *C.LIBMTP_mtpdevice_t
	label     string
	folderIDs map[string]C.uint32_t // "storageID/dir/sub" → object id
}

// MTPManager owns libmtp device handles and serializes all libmtp access.
type MTPManager struct {
	mu   sync.Mutex
	open map[string]*openDevice // key → handle
}

func NewMTPManager() *MTPManager {
	return &MTPManager{open: map[string]*openDevice{}}
}

// Detect rescans USB and returns the current device list. It is claim-free: it
// only reads USB descriptors (via LIBMTP_Detect_Raw_Devices) and never opens a
// handle, so it is cheap to poll and works even when a desktop MTP daemon holds
// the device. Opening (which requires an exclusive interface claim) is deferred
// to Open. Handles for devices that have vanished are released here.
func (m *MTPManager) Detect() []MTPDevice {
	mtpInitOnce.Do(func() { C.LIBMTP_Init() })

	m.mu.Lock()
	defer m.mu.Unlock()

	var rawList *C.LIBMTP_raw_device_t
	var count C.int
	if C.LIBMTP_Detect_Raw_Devices(&rawList, &count) != C.LIBMTP_ERROR_NONE || rawList == nil {
		m.releaseAllLocked()
		return nil
	}
	defer C.free(unsafe.Pointer(rawList))

	raws := unsafe.Slice(rawList, int(count))
	present := make(map[string]struct{}, len(raws))
	out := make([]MTPDevice, 0, len(raws))
	for i := range raws {
		key := rawKey(&raws[i])
		present[key] = struct{}{}
		label := rawLabel(&raws[i])
		if dev, ok := m.open[key]; ok && dev.label != "" {
			label = dev.label // prefer the friendly name once the device is open
		}
		out = append(out, MTPDevice{Key: key, Label: label})
	}
	// Release handles for devices no longer present.
	for key, dev := range m.open {
		if _, ok := present[key]; !ok {
			C.LIBMTP_Release_Device(dev.handle)
			delete(m.open, key)
		}
	}
	return out
}

// Open claims a device handle so its storages can be listed and files
// transferred. It is a no-op if the device is already open. It returns a
// non-nil error on any libmtp failure — most commonly because a resident
// desktop MTP daemon (gvfs or KDE) already holds the exclusive USB interface
// claim. libmtp does not expose the underlying errno, so the caller cannot
// distinguish "busy" from other failures and should treat any error as a
// signal to try an alternate backend.
func (m *MTPManager) Open(key string) error {
	mtpInitOnce.Do(func() { C.LIBMTP_Init() })

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.open[key]; ok {
		return nil
	}

	var rawList *C.LIBMTP_raw_device_t
	var count C.int
	if C.LIBMTP_Detect_Raw_Devices(&rawList, &count) != C.LIBMTP_ERROR_NONE || rawList == nil {
		return errors.New("mtp: no devices detected")
	}
	defer C.free(unsafe.Pointer(rawList))

	raws := unsafe.Slice(rawList, int(count))
	for i := range raws {
		if rawKey(&raws[i]) != key {
			continue
		}
		h := C.LIBMTP_Open_Raw_Device_Uncached(&raws[i])
		if h == nil {
			return errors.New("mtp: cannot open device " + key + " (busy or unavailable; a desktop MTP daemon may hold it)")
		}
		m.open[key] = &openDevice{
			handle:    h,
			label:     deviceLabel(h, &raws[i]),
			folderIDs: map[string]C.uint32_t{},
		}
		return nil
	}
	return errors.New("mtp: device not connected: " + key)
}

// Close releases a single device handle (used on eject).
func (m *MTPManager) Close(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if dev, ok := m.open[key]; ok {
		C.LIBMTP_Release_Device(dev.handle)
		delete(m.open, key)
	}
}

func (m *MTPManager) releaseAllLocked() {
	for key, dev := range m.open {
		C.LIBMTP_Release_Device(dev.handle)
		delete(m.open, key)
	}
}

// rawKey is the stable-while-connected device key "bus-devnum".
func rawKey(raw *C.LIBMTP_raw_device_t) string {
	return fmt.Sprintf("%d-%d", uint32(raw.bus_location), uint8(raw.devnum))
}

// rawLabel derives a label from the USB descriptor alone (no open handle).
func rawLabel(raw *C.LIBMTP_raw_device_t) string {
	vendor := C.GoString(raw.device_entry.vendor)
	product := C.GoString(raw.device_entry.product)
	label := strings.TrimSpace(vendor + " " + product)
	if label == "" {
		return "MTP device"
	}
	return label
}

func deviceLabel(h *C.LIBMTP_mtpdevice_t, raw *C.LIBMTP_raw_device_t) string {
	if cn := C.LIBMTP_Get_Friendlyname(h); cn != nil {
		name := C.GoString(cn)
		C.free(unsafe.Pointer(cn))
		if strings.TrimSpace(name) != "" {
			return name
		}
	}
	return rawLabel(raw)
}

// --- MTPFS: the FileSystem/Importer/DirMaker over MTPManager ---

type MTPFS struct {
	m      *MTPManager
	tmpDir string
}

func NewMTPFS(m *MTPManager) *MTPFS { return &MTPFS{m: m} }

func (f *MTPFS) Scheme() string { return "mtp" }

// mtpParts splits an "mtp:" URI into device key and remaining path segments
// (the first of which, when present, is the numeric storage id).
func mtpParts(uri string) (devKey string, segs []string) {
	p := strings.TrimPrefix(uri, mtpScheme)
	p = strings.Trim(p, "/")
	if p == "" {
		return "", nil
	}
	all := strings.Split(p, "/")
	return all[0], all[1:]
}

func mtpURI(segs ...string) string {
	return mtpScheme + "/" + strings.Join(segs, "/")
}

func (f *MTPFS) List(uri string) ([]Entry, error) {
	devKey, segs := mtpParts(uri)
	if devKey == "" {
		// Root: list connected devices.
		devs := f.m.Detect()
		entries := make([]Entry, 0, len(devs))
		for _, d := range devs {
			entries = append(entries, Entry{Name: d.Label, Path: mtpURI(d.Key), IsDir: true})
		}
		return entries, nil
	}
	// Any device-level navigation needs a claimed handle; open on demand
	// (no-op if the sidebar already opened it).
	if err := f.m.Open(devKey); err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		// Device root: list storages.
		stores, err := f.m.listStorages(devKey)
		if err != nil {
			return nil, err
		}
		entries := make([]Entry, 0, len(stores))
		for _, s := range stores {
			entries = append(entries, Entry{
				Name:  s.desc,
				Path:  mtpURI(devKey, strconv.FormatUint(uint64(s.id), 10)),
				IsDir: true,
			})
		}
		return entries, nil
	}
	storageID, err := parseStorageID(segs[0])
	if err != nil {
		return nil, err
	}
	items, err := f.m.listFolder(devKey, storageID, segs[1:])
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	childBase := append([]string{devKey}, segs...)
	for _, it := range items {
		entries = append(entries, Entry{
			Name:    it.name,
			Path:    mtpURI(append(append([]string{}, childBase...), it.name)...),
			IsDir:   it.isDir,
			Size:    it.size,
			ModTime: it.mod,
			Hash:    strconv.FormatUint(uint64(it.id), 10),
		})
	}
	return entries, nil
}

func (f *MTPFS) Materialize(e Entry) (string, error) {
	if e.Hash == "" {
		return "", errors.New("mtp: entry has no object id: " + e.Name)
	}
	devKey, _ := mtpParts(e.Path)
	objID, err := strconv.ParseUint(e.Hash, 10, 32)
	if err != nil {
		return "", err
	}
	if f.tmpDir == "" {
		d, err := os.MkdirTemp("", "tie-fm-mtp-")
		if err != nil {
			return "", err
		}
		f.tmpDir = d
	}
	dest := filepath.Join(f.tmpDir, e.Hash+"-"+e.Name)
	if _, err := os.Stat(dest); err == nil {
		return dest, nil
	}
	if err := f.m.download(devKey, C.uint32_t(objID), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Import uploads a local file to destDir on the device, creating any missing
// folders. Implements Importer.
func (f *MTPFS) Import(destDir, srcPath, name string) error {
	devKey, segs := mtpParts(destDir)
	if devKey == "" || len(segs) == 0 {
		return errors.New("mtp: import destination must include a storage: " + destDir)
	}
	storageID, err := parseStorageID(segs[0])
	if err != nil {
		return err
	}
	parentID, err := f.m.resolveFolder(devKey, storageID, segs[1:], true)
	if err != nil {
		return err
	}
	return f.m.upload(devKey, storageID, parentID, name, srcPath)
}

// Mkdir creates a folder at parent/name on the device. Implements DirMaker.
func (f *MTPFS) Mkdir(parent, name string) error {
	devKey, segs := mtpParts(parent)
	if devKey == "" || len(segs) == 0 {
		return errors.New("mtp: cannot create a folder outside a storage: " + parent)
	}
	storageID, err := parseStorageID(segs[0])
	if err != nil {
		return err
	}
	parentID, err := f.m.resolveFolder(devKey, storageID, segs[1:], true)
	if err != nil {
		return err
	}
	_, err = f.m.mkdir(devKey, storageID, parentID, name)
	return err
}

func parseStorageID(s string) (C.uint32_t, error) {
	v, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("mtp: bad storage id %q: %w", s, err)
	}
	return C.uint32_t(v), nil
}

// --- libmtp calls (all under the manager mutex) ---

type storageInfo struct {
	id   uint32
	desc string
}

type mtpItem struct {
	id    uint32
	name  string
	isDir bool
	size  int64
	mod   time.Time
}

func (m *MTPManager) deviceLocked(key string) (*openDevice, error) {
	dev, ok := m.open[key]
	if !ok {
		return nil, errors.New("mtp: device not connected: " + key)
	}
	return dev, nil
}

func (m *MTPManager) listStorages(key string) ([]storageInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return nil, err
	}
	C.LIBMTP_Get_Storage(dev.handle, C.LIBMTP_STORAGE_SORTBY_NOTSORTED)
	var out []storageInfo
	for s := dev.handle.storage; s != nil; s = s.next {
		desc := C.GoString(s.StorageDescription)
		if strings.TrimSpace(desc) == "" {
			desc = "Storage " + strconv.FormatUint(uint64(uint32(s.id)), 10)
		}
		out = append(out, storageInfo{id: uint32(s.id), desc: desc})
	}
	return out, nil
}

func (m *MTPManager) listFolder(key string, storageID C.uint32_t, folders []string) ([]mtpItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return nil, err
	}
	parentID, err := m.resolveFolderLocked(dev, storageID, folders, false)
	if err != nil {
		return nil, err
	}
	return listFolderLocked(dev, storageID, parentID), nil
}

func listFolderLocked(dev *openDevice, storageID, parentID C.uint32_t) []mtpItem {
	list := C.LIBMTP_Get_Files_And_Folders(dev.handle, storageID, parentID)
	var out []mtpItem
	for f := list; f != nil; {
		isDir := f.filetype == C.LIBMTP_FILETYPE_FOLDER
		out = append(out, mtpItem{
			id:    uint32(f.item_id),
			name:  C.GoString(f.filename),
			isDir: isDir,
			size:  int64(f.filesize),
			mod:   time.Unix(int64(f.modificationdate), 0),
		})
		next := f.next
		C.LIBMTP_destroy_file_t(f)
		f = next
	}
	return out
}

// resolveFolder maps a folder path (under a storage) to its object id, using and
// filling the per-device cache. When create is true, missing folders are made.
func (m *MTPManager) resolveFolder(key string, storageID C.uint32_t, folders []string, create bool) (C.uint32_t, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return 0, err
	}
	return m.resolveFolderLocked(dev, storageID, folders, create)
}

func (m *MTPManager) resolveFolderLocked(dev *openDevice, storageID C.uint32_t, folders []string, create bool) (C.uint32_t, error) {
	parent := C.uint32_t(C.LIBMTP_FILES_AND_FOLDERS_ROOT)
	cacheKey := strconv.FormatUint(uint64(uint32(storageID)), 10)
	for _, name := range folders {
		cacheKey += "/" + name
		if id, ok := dev.folderIDs[cacheKey]; ok {
			parent = id
			continue
		}
		found := C.uint32_t(0)
		for _, it := range listFolderLocked(dev, storageID, parent) {
			if it.isDir && it.name == name {
				found = C.uint32_t(it.id)
				break
			}
		}
		if found == 0 {
			if !create {
				return 0, errors.New("mtp: folder not found: " + name)
			}
			id, err := mkdirLocked(dev, storageID, parent, name)
			if err != nil {
				return 0, err
			}
			found = id
		}
		dev.folderIDs[cacheKey] = found
		parent = found
	}
	return parent, nil
}

func (m *MTPManager) download(key string, objID C.uint32_t, dest string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return err
	}
	cdest := C.CString(dest)
	defer C.free(unsafe.Pointer(cdest))
	if C.LIBMTP_Get_File_To_File(dev.handle, objID, cdest, nil, nil) != 0 {
		return errors.New("mtp: download failed for object " + strconv.FormatUint(uint64(uint32(objID)), 10))
	}
	return nil
}

func (m *MTPManager) upload(key string, storageID, parentID C.uint32_t, name, srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return err
	}
	meta := C.LIBMTP_new_file_t()
	defer C.LIBMTP_destroy_file_t(meta)
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	meta.filename = cname
	meta.filesize = C.uint64_t(info.Size())
	meta.filetype = C.LIBMTP_FILETYPE_UNKNOWN
	meta.parent_id = parentID
	meta.storage_id = storageID

	csrc := C.CString(srcPath)
	defer C.free(unsafe.Pointer(csrc))
	if C.LIBMTP_Send_File_From_File(dev.handle, csrc, meta, nil, nil) != 0 {
		return errors.New("mtp: upload failed for " + name)
	}
	return nil
}

func (m *MTPManager) mkdir(key string, storageID, parentID C.uint32_t, name string) (C.uint32_t, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dev, err := m.deviceLocked(key)
	if err != nil {
		return 0, err
	}
	return mkdirLocked(dev, storageID, parentID, name)
}

func mkdirLocked(dev *openDevice, storageID, parentID C.uint32_t, name string) (C.uint32_t, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	id := C.LIBMTP_Create_Folder(dev.handle, cname, parentID, storageID)
	if id == 0 {
		return 0, errors.New("mtp: could not create folder " + name)
	}
	return id, nil
}
