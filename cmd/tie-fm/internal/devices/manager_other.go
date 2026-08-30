//go:build !linux

package devices

import (
	"errors"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
)

// Manager is a no-op on platforms without udisks2/libmtp support.
type Manager struct{ OnChange func() }

func NewManager(*fs.MTPManager) *Manager { return &Manager{} }

func (m *Manager) Start()                        {}
func (m *Manager) Devices() []Device             { return nil }
func (m *Manager) Mount(Device) (string, error)  { return "", errors.New("removable devices not supported on this platform") }
func (m *Manager) Eject(Device) error            { return errors.New("removable devices not supported on this platform") }
