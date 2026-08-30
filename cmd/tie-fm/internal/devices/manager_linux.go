//go:build linux

package devices

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

// mtpPoll is how often MTP devices are re-detected; libmtp has no hotplug event
// API, so a light periodic raw scan (which does not open handles) is used.
const mtpPoll = 2 * time.Second

// Manager coalesces udisks2 block devices and MTP devices into one live list.
type Manager struct {
	mtp     *fs.MTPManager
	udisks  *udisksClient
	udErr   error
	gvfs    *gvfsClient
	OnChange func()

	mu         sync.Mutex
	snap       []Device
	blocks     map[string]blockDevice // Device.Key → udisks device (for mount/eject)
	gvfsMounts map[string]gvfsMount   // MTP Device.Key → active gvfs fallback mount
}

// NewManager builds a device manager over the shared MTP manager (also used by
// the mtp: filesystem provider).
func NewManager(mtp *fs.MTPManager) *Manager {
	u, err := newUDisks()
	g, _ := newGVFS() // absence is not fatal; the gvfs fallback is simply unavailable
	return &Manager{
		mtp:        mtp,
		udisks:     u,
		udErr:      err,
		gvfs:       g,
		blocks:     map[string]blockDevice{},
		gvfsMounts: map[string]gvfsMount{},
	}
}

// Start performs an initial scan, subscribes to udisks2 change signals, and
// begins polling for MTP devices.
func (m *Manager) Start() {
	m.refresh()
	if m.udisks != nil {
		_ = m.udisks.watch(m.refresh)
	}
	go func() {
		t := time.NewTicker(mtpPoll)
		defer t.Stop()
		for range t.C {
			m.refresh()
		}
	}()
}

// Devices returns the current snapshot.
func (m *Manager) Devices() []Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Device, len(m.snap))
	copy(out, m.snap)
	return out
}

// Mount ensures the device is mounted and returns the path to navigate to.
func (m *Manager) Mount(d Device) (string, error) {
	if d.Kind == MTP {
		return m.mountMTP(d)
	}
	if m.udisks == nil {
		return "", errUDisksUnavailable(m.udErr)
	}
	m.mu.Lock()
	bd, ok := m.blocks[d.Key]
	m.mu.Unlock()
	if !ok {
		return "", errors.New("device no longer connected")
	}
	path, err := m.udisks.Mount(bd)
	if err != nil {
		return "", err
	}
	m.refresh()
	return path, nil
}

// mountMTP selects an MTP backend at open time: libmtp first (works on bare
// window managers where nothing claims the device), then gvfs as a fallback
// (works on GNOME-family desktops where a daemon holds the device but exposes
// it as a FUSE path). libmtp cannot report why an open failed, so any failure
// triggers the gvfs attempt.
func (m *Manager) mountMTP(d Device) (string, error) {
	if err := m.mtp.Open(d.Key); err == nil {
		return d.Path, nil // navigate the mtp: provider
	}
	if m.gvfs == nil || !m.gvfs.available() {
		return "", errors.New("cannot open MTP device: it is held by another program and gvfs is unavailable")
	}
	gm, err := m.gvfs.mount(d.Key)
	if err != nil {
		return "", errors.New("cannot open MTP device: libmtp is blocked (a desktop MTP daemon holds it) and the gvfs fallback failed: " + err.Error())
	}
	m.mu.Lock()
	m.gvfsMounts[d.Key] = gm
	m.mu.Unlock()
	return gm.path, nil // navigate the local FUSE path via LocalFS
}

// Eject unmounts a block device (and powers it off) or releases an MTP handle.
func (m *Manager) Eject(d Device) error {
	if d.Kind == MTP {
		m.mu.Lock()
		gm, viaGVFS := m.gvfsMounts[d.Key]
		m.mu.Unlock()
		if viaGVFS {
			if err := m.gvfs.unmount(gm); err != nil {
				return err
			}
			m.mu.Lock()
			delete(m.gvfsMounts, d.Key)
			m.mu.Unlock()
		} else {
			m.mtp.Close(d.Key)
		}
		m.refresh()
		return nil
	}
	if m.udisks == nil {
		return errUDisksUnavailable(m.udErr)
	}
	m.mu.Lock()
	bd, ok := m.blocks[d.Key]
	m.mu.Unlock()
	if !ok {
		return errors.New("device no longer connected")
	}
	if err := m.udisks.Eject(bd); err != nil {
		return err
	}
	m.refresh()
	return nil
}

// refresh rebuilds the device snapshot from both backends and notifies the UI.
func (m *Manager) refresh() {
	var devs []Device
	blocks := map[string]blockDevice{}

	if m.udisks != nil {
		if list, err := m.udisks.listRemovable(); err == nil {
			for _, bd := range list {
				key := string(bd.obj)
				blocks[key] = bd
				devs = append(devs, Device{
					Key:     key,
					Label:   bd.label,
					Kind:    Block,
					Path:    bd.mountPoint,
					Mounted: bd.mountPoint != "",
				})
			}
		}
	}
	for _, d := range m.mtp.Detect() {
		devs = append(devs, Device{
			Key:     d.Key,
			Label:   d.Label,
			Kind:    MTP,
			Path:    "mtp:/" + d.Key,
			Mounted: false, // opened lazily on click (libmtp or gvfs fallback)
		})
	}
	sort.Slice(devs, func(i, j int) bool {
		if devs[i].Kind != devs[j].Kind {
			return devs[i].Kind < devs[j].Kind
		}
		return devs[i].Label < devs[j].Label
	})

	m.mu.Lock()
	m.snap = devs
	m.blocks = blocks
	m.mu.Unlock()

	if m.OnChange != nil {
		m.OnChange()
	}
}

func errUDisksUnavailable(err error) error {
	if err != nil {
		return errors.New("udisks2 unavailable: " + err.Error())
	}
	return errors.New("udisks2 unavailable")
}
