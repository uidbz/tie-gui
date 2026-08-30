//go:build linux

package devices

// gvfs MTP fallback: when libmtp cannot claim a device (a resident desktop MTP
// daemon holds the exclusive USB interface), browse the phone through gvfs
// instead. gvfs mounts the device and gvfsd-fuse exposes it under
// /run/user/$UID/gvfs/mtp:host=… as an ordinary POSIX path, which tie-fm's
// LocalFS then lists and copies to/from like any local directory.
//
// This drives the gvfs volume monitor over its private D-Bus interface
// (org.gtk.Private.RemoteVolumeMonitor on the session bus) rather than shelling
// out to `gio`. A device is matched to a gvfs volume by its USB node
// (/dev/bus/usb/BBB/DDD), which the volume publishes as its "unix-device"
// identifier.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	mtpMonitorService = "org.gtk.vfs.MTPVolumeMonitor"
	remoteVolMonPath  = "/org/gtk/Private/RemoteVolumeMonitor"
	ifaceRemoteVolMon = "org.gtk.Private.RemoteVolumeMonitor"
)

// gvfsMount records a live gvfs mount so it can later be unmounted.
type gvfsMount struct {
	path    string // FUSE path under /run/user/$UID/gvfs
	mountID string // RemoteVolumeMonitor mount id, for MountUnmount
}

type gvfsClient struct {
	conn *dbus.Conn
}

// D-Bus struct layouts for RemoteVolumeMonitor.List, matched positionally by
// godbus. Only the volume fields are used; drive/mount records are decoded
// solely so the three-tuple return can be stored.

// (ssssssbbssa{ss}sa{sv})
type gvfsVolumeRec struct {
	ID              string
	Name            string
	GIcon           string
	SymbolicGIcon   string
	UUID            string
	ActivationRoot  string
	CanMount        bool
	ShouldAutomount bool
	DriveID         string
	MountID         string
	Identifiers     map[string]string
	SortKey         string
	Expansion       map[string]dbus.Variant
}

// (ssssbbbbbbbbuasa{ss}sa{sv})
type gvfsDriveRec struct {
	ID            string
	Name          string
	GIcon         string
	SymbolicGIcon string
	B1, B2, B3, B4, B5, B6, B7, B8 bool
	U             uint32
	VolumeIDs     []string
	Identifiers   map[string]string
	SortKey       string
	Expansion     map[string]dbus.Variant
}

// (ssssssbsassa{sv})
type gvfsMountRec struct {
	ID            string
	Name          string
	GIcon         string
	SymbolicGIcon string
	UUID          string
	Root          string
	CanUnmount    bool
	VolumeID      string
	XContentTypes []string
	SortKey       string
	PreferredName string
	Expansion     map[string]dbus.Variant
}

func newGVFS() (*gvfsClient, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	return &gvfsClient{conn: conn}, nil
}

// available reports whether the gvfs MTP volume monitor is present on the
// session bus and usable.
func (g *gvfsClient) available() bool {
	if g == nil || g.conn == nil {
		return false
	}
	obj := g.conn.Object(mtpMonitorService, remoteVolMonPath)
	var supported bool
	if err := obj.Call(ifaceRemoteVolMon+".IsSupported", 0).Store(&supported); err != nil {
		return false
	}
	return supported
}

func (g *gvfsClient) listVolumes() ([]gvfsVolumeRec, error) {
	obj := g.conn.Object(mtpMonitorService, remoteVolMonPath)
	var drives []gvfsDriveRec
	var volumes []gvfsVolumeRec
	var mounts []gvfsMountRec
	if err := obj.Call(ifaceRemoteVolMon+".List", 0).Store(&drives, &volumes, &mounts); err != nil {
		return nil, err
	}
	return volumes, nil
}

// mount ensures the MTP device identified by busDevKey ("bus-devnum") is mounted
// via gvfs and returns its FUSE path. It is idempotent: an already-mounted
// device just has its path resolved.
func (g *gvfsClient) mount(busDevKey string) (gvfsMount, error) {
	node, err := usbNode(busDevKey)
	if err != nil {
		return gvfsMount{}, err
	}
	vol, err := g.findVolume(node)
	if err != nil {
		return gvfsMount{}, err
	}
	if vol.MountID == "" {
		obj := g.conn.Object(mtpMonitorService, remoteVolMonPath)
		call := obj.Call(ifaceRemoteVolMon+".VolumeMount", 0, vol.ID, "", uint32(0), "")
		if call.Err != nil {
			return gvfsMount{}, fmt.Errorf("gvfs: mounting %s failed: %w", vol.Name, call.Err)
		}
		if vol, err = g.findVolume(node); err != nil {
			return gvfsMount{}, err
		}
	}
	path, err := g.resolveFusePath(vol.ActivationRoot)
	if err != nil {
		return gvfsMount{}, err
	}
	return gvfsMount{path: path, mountID: vol.MountID}, nil
}

func (g *gvfsClient) unmount(m gvfsMount) error {
	if m.mountID == "" {
		return nil
	}
	obj := g.conn.Object(mtpMonitorService, remoteVolMonPath)
	return obj.Call(ifaceRemoteVolMon+".MountUnmount", 0, m.mountID, "", uint32(0), "").Err
}

func (g *gvfsClient) findVolume(node string) (gvfsVolumeRec, error) {
	volumes, err := g.listVolumes()
	if err != nil {
		return gvfsVolumeRec{}, err
	}
	for _, v := range volumes {
		if v.Identifiers["unix-device"] == node {
			return v, nil
		}
	}
	return gvfsVolumeRec{}, fmt.Errorf("gvfs: no MTP volume for %s", node)
}

// resolveFusePath waits for gvfsd-fuse to expose the mount and returns its POSIX
// path. The fuse directory is named mtp:host=<encoded activation-root host>; the
// host is matched after percent-decoding so encoding differences don't matter.
func (g *gvfsClient) resolveFusePath(activationRoot string) (string, error) {
	host, err := mtpHost(activationRoot)
	if err != nil {
		return "", err
	}
	base := filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "gvfs")
	// The mount reply can precede gvfsd-fuse exposing the path; poll briefly.
	var lastErr error
	for i := 0; i < 20; i++ {
		p, err := findMTPFuseDir(base, host)
		if err == nil {
			return p, nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return "", lastErr
}

// findMTPFuseDir scans base for the mtp:host=… entry whose decoded host matches.
func findMTPFuseDir(base, host string) (string, error) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return "", fmt.Errorf("gvfs: fuse mount unavailable (gvfsd-fuse not running?): %w", err)
	}
	var mtpEntries []string
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, "mtp:host=") {
			continue
		}
		mtpEntries = append(mtpEntries, name)
		enc := strings.TrimPrefix(name, "mtp:host=")
		if dec, err := url.PathUnescape(enc); err == nil && dec == host {
			return filepath.Join(base, name), nil
		}
	}
	// Fall back to the sole MTP mount when the host can't be matched exactly.
	if len(mtpEntries) == 1 {
		return filepath.Join(base, mtpEntries[0]), nil
	}
	return "", errors.New("gvfs: mount not yet visible under " + base)
}

// mtpHost extracts the host of an "mtp://HOST/" activation root. It trims the
// scheme rather than using net/url, because gvfs also uses a bracketed host form
// ("mtp://[usb:008,030]/") that url.Parse misreads as an IPv6 literal.
func mtpHost(activationRoot string) (string, error) {
	const scheme = "mtp://"
	if !strings.HasPrefix(activationRoot, scheme) {
		return "", fmt.Errorf("gvfs: bad activation root %q", activationRoot)
	}
	host := strings.TrimPrefix(activationRoot, scheme)
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i] // drop any path after the authority
	}
	if host == "" {
		return "", fmt.Errorf("gvfs: activation root %q has no host", activationRoot)
	}
	return host, nil
}

// usbNode converts a "bus-devnum" key into the /dev/bus/usb/BBB/DDD node that
// gvfs volumes publish as their "unix-device" identifier.
func usbNode(busDevKey string) (string, error) {
	bus, dev, ok := strings.Cut(busDevKey, "-")
	if !ok {
		return "", fmt.Errorf("gvfs: bad device key %q", busDevKey)
	}
	b, err := strconv.Atoi(bus)
	if err != nil {
		return "", fmt.Errorf("gvfs: bad bus in key %q: %w", busDevKey, err)
	}
	d, err := strconv.Atoi(dev)
	if err != nil {
		return "", fmt.Errorf("gvfs: bad devnum in key %q: %w", busDevKey, err)
	}
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", b, d), nil
}
