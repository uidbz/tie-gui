// Package devices discovers removable storage — USB block devices (via udisks2)
// and MTP media devices such as phones (via the fs MTP backend) — and presents
// them uniformly so the UI can list, mount, browse, and eject them.
package devices

// Kind distinguishes how a device is reached.
type Kind int

const (
	Block Kind = iota // udisks2-managed block filesystem (USB stick, SD card)
	MTP               // MTP media device (phone, camera) under the mtp: scheme
)

// Device is one entry in the sidebar's Devices section.
type Device struct {
	Key     string // stable identifier while connected
	Label   string
	Kind    Kind
	Path    string // navigation URI once mounted; "" for an unmounted block device
	Mounted bool
}
