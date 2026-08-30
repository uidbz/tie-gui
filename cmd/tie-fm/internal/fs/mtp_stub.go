//go:build !(linux && cgo)

package fs

import "errors"

var errMTPUnsupported = errors.New("mtp: not supported in this build (needs linux + cgo + libmtp)")

// MTPDevice mirrors the cgo build's type so the sidebar compiles everywhere.
type MTPDevice struct {
	Key   string
	Label string
}

// MTPManager is a no-op when built without libmtp.
type MTPManager struct{}

func NewMTPManager() *MTPManager { return &MTPManager{} }

func (m *MTPManager) Detect() []MTPDevice { return nil }
func (m *MTPManager) Open(string) error   { return errMTPUnsupported }
func (m *MTPManager) Close(string)        {}

// MTPFS reports the feature as unavailable rather than being absent, so the
// registry wiring in main.go is identical across builds.
type MTPFS struct{}

func NewMTPFS(*MTPManager) *MTPFS { return &MTPFS{} }

func (f *MTPFS) Scheme() string                    { return "mtp" }
func (f *MTPFS) List(string) ([]Entry, error)      { return nil, errMTPUnsupported }
func (f *MTPFS) Materialize(Entry) (string, error) { return "", errMTPUnsupported }
func (f *MTPFS) Import(string, string, string) error { return errMTPUnsupported }
func (f *MTPFS) Mkdir(string, string) error        { return errMTPUnsupported }
