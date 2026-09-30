//go:build !linux || android

package mpris

// Server is inert where there is no D-Bus session bus.
type Server struct{}

// Start always fails here: MPRIS is a Linux desktop (D-Bus) interface.
func Start(Controller) (*Server, error) { return nil, ErrUnsupported }

// Name is empty on unsupported platforms.
func (s *Server) Name() string { return "" }

// Close is a no-op.
func (s *Server) Close() error { return nil }

// Update is a no-op.
func (s *Server) Update(State) {}

// Control always fails here.
func Control(action string) error {
	if _, ok := methodForAction(action); !ok {
		return ErrUnsupported
	}
	return ErrUnsupported
}
