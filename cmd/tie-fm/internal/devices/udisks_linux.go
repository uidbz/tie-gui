//go:build linux

package devices

import (
	"bytes"
	"errors"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	udisksService  = "org.freedesktop.UDisks2"
	udisksObjMgr   = "/org/freedesktop/UDisks2"
	ifaceFS        = "org.freedesktop.UDisks2.Filesystem"
	ifaceBlock     = "org.freedesktop.UDisks2.Block"
	ifaceDrive     = "org.freedesktop.UDisks2.Drive"
	ifaceObjMgr    = "org.freedesktop.DBus.ObjectManager"
	ifacePropsName = "org.freedesktop.DBus.Properties"
)

// blockDevice is a removable udisks2 filesystem that can be mounted and browsed.
type blockDevice struct {
	obj        dbus.ObjectPath
	drive      dbus.ObjectPath
	label      string
	mountPoint string // "" when not mounted
}

type udisksClient struct {
	conn *dbus.Conn
}

func newUDisks() (*udisksClient, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return &udisksClient{conn: conn}, nil
}

type managedObjects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func (u *udisksClient) managed() (managedObjects, error) {
	if u.conn == nil {
		return nil, errors.New("udisks: connection closed")
	}
	var objs managedObjects
	obj := u.conn.Object(udisksService, udisksObjMgr)
	err := obj.Call(ifaceObjMgr+".GetManagedObjects", 0).Store(&objs)
	return objs, err
}

// listRemovable returns removable/USB filesystems currently known to udisks2.
func (u *udisksClient) listRemovable() ([]blockDevice, error) {
	objs, err := u.managed()
	if err != nil {
		return nil, err
	}
	var out []blockDevice
	for objPath, ifaces := range objs {
		fsIface, ok := ifaces[ifaceFS]
		if !ok {
			continue
		}
		block := ifaces[ifaceBlock]
		if block == nil {
			continue
		}
		if b, ok := variantBool(block["HintSystem"]); ok && b {
			continue
		}
		driveePath, _ := block["Drive"].Value().(dbus.ObjectPath)
		if !u.driveIsRemovable(objs, driveePath) {
			continue
		}
		out = append(out, blockDevice{
			obj:        objPath,
			drive:      driveePath,
			label:      blockLabel(objs, block, driveePath),
			mountPoint: firstMountPoint(fsIface["MountPoints"]),
		})
	}
	return out, nil
}

func (u *udisksClient) driveIsRemovable(objs managedObjects, drive dbus.ObjectPath) bool {
	if drive == "" || drive == "/" {
		return false
	}
	d := objs[drive][ifaceDrive]
	if d == nil {
		return false
	}
	if rm, ok := variantBool(d["Removable"]); ok && rm {
		return true
	}
	bus, _ := d["ConnectionBus"].Value().(string)
	return bus == "usb"
}

// Mount ensures the device is mounted and returns the mount path.
func (u *udisksClient) Mount(bd blockDevice) (string, error) {
	// Don't trust the snapshot's mountPoint: the device may have been
	// unmounted (or auto-mounted elsewhere) between the scan and the click.
	// Ask udisks to mount, and resolve the live path from managed() when the
	// call reports "already mounted" or returns no path.
	if u.conn == nil {
		return "", errors.New("udisks: connection closed")
	}
	obj := u.conn.Object(udisksService, bd.obj)
	var mountPath string
	err := obj.Call(ifaceFS+".Mount", 0, map[string]dbus.Variant{}).Store(&mountPath)
	if err == nil && mountPath != "" {
		return mountPath, nil
	}
	if objs, merr := u.managed(); merr == nil {
		if ifaces, ok := objs[bd.obj]; ok {
			if mp := firstMountPoint(ifaces[ifaceFS]["MountPoints"]); mp != "" {
				return mp, nil
			}
		}
	}
	if err != nil {
		return "", err
	}
	return "", errors.New("udisks: mount succeeded but reported no mount path")
}

// Eject unmounts the filesystem and powers the drive off (best effort), so the
// stick can be physically removed.
func (u *udisksClient) Eject(bd blockDevice) error {
	if u.conn == nil {
		return errors.New("udisks: connection closed")
	}
	fsObj := u.conn.Object(udisksService, bd.obj)
	if call := fsObj.Call(ifaceFS+".Unmount", 0, map[string]dbus.Variant{}); call.Err != nil {
		// Not fatal if it was already unmounted; keep going to power off.
		if !errors.Is(call.Err, dbus.ErrMsgNoObject) {
			return call.Err
		}
	}
	if bd.drive != "" && bd.drive != "/" {
		drv := u.conn.Object(udisksService, bd.drive)
		drv.Call(ifaceDrive+".PowerOff", 0, map[string]dbus.Variant{})
	}
	return nil
}

// watch invokes onChange whenever udisks2 reports device or mount changes.
// The matches are restricted to the udisks2 sender (and its manager object for
// the ObjectManager signals): an unfiltered PropertiesChanged match fires for
// every property change on the whole system bus, turning a busy desktop into
// a refresh storm (each one runs a GetManagedObjects + libmtp rescan).
func (u *udisksClient) watch(onChange func()) error {
	if err := u.conn.AddMatchSignal(
		dbus.WithMatchInterface(ifaceObjMgr),
		dbus.WithMatchObjectPath(udisksObjMgr),
		dbus.WithMatchSender(udisksService),
	); err != nil {
		return err
	}
	if err := u.conn.AddMatchSignal(
		dbus.WithMatchInterface(ifacePropsName),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchSender(udisksService),
	); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, 16)
	u.conn.Signal(ch)
	go func() {
		for range ch {
			onChange()
		}
	}()
	return nil
}

// close removes the signal subscription and closes the system-bus connection.
func (u *udisksClient) close() {
	if u.conn == nil {
		return
	}
	u.conn.RemoveMatchSignal(
		dbus.WithMatchInterface(ifaceObjMgr),
		dbus.WithMatchObjectPath(udisksObjMgr),
		dbus.WithMatchSender(udisksService),
	)
	u.conn.RemoveMatchSignal(
		dbus.WithMatchInterface(ifacePropsName),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchSender(udisksService),
	)
	u.conn.Close()
	u.conn = nil
}

func blockLabel(objs managedObjects, block map[string]dbus.Variant, drive dbus.ObjectPath) string {
	if l, _ := block["IdLabel"].Value().(string); l != "" {
		return l
	}
	if d := objs[drive][ifaceDrive]; d != nil {
		vendor, _ := d["Vendor"].Value().(string)
		model, _ := d["Model"].Value().(string)
		if label := strings.TrimSpace(vendor + " " + model); label != "" {
			return label
		}
	}
	if dev := decodeByteString(block["Device"]); dev != "" {
		return dev
	}
	return "USB storage"
}

func variantBool(v dbus.Variant) (bool, bool) {
	b, ok := v.Value().(bool)
	return b, ok
}

// decodeByteString decodes a udisks2 bytestring property (NUL-terminated),
// e.g. the Block "Device" node.
func decodeByteString(v dbus.Variant) string {
	switch val := v.Value().(type) {
	case []byte:
		return string(bytes.TrimRight(val, "\x00"))
	case [][]byte:
		if len(val) == 0 {
			return ""
		}
		return string(bytes.TrimRight(val[0], "\x00"))
	}
	return ""
}

// firstMountPoint decodes a udisks2 bytestring property (NUL-terminated) or the
// first entry of an array of them.
func firstMountPoint(v dbus.Variant) string {
	return decodeByteString(v)
}
