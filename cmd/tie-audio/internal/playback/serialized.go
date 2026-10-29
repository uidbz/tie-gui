package playback

import (
	"io"
	"sync"
)

// The PlaybackBackend contract says implementations run "one action at a
// time" — but the UI issues actions from fresh goroutines per click (the
// transport's do(), the album page's play/enqueue, queue move/remove
// commits), and the transport status poll runs continuously beside them.
// Interleaved, a PlayAlbum's settle waits see foreign mutations and time
// out, MoveItems lands on indices that no longer hold the dragged block,
// and polls render half-applied states.
//
// Serialized wraps a backend and runs every method call one at a time, so
// the contract holds regardless of how many goroutines issue actions. A
// long-running compound action (PlayAlbum's replace-and-settle) makes
// concurrent Status polls wait — which is exactly what the queue view
// wants (it learns the settled state as soon as the mutation is done
// instead of racing it).
//
// Construction wraps the session's backend once (data.NewSession), so
// every holder — transport, queue page, album page, history recorder,
// MPRIS bridge — shares the same serialization without rewiring anything.

// Serialized returns a backend whose methods run mutually exclusively.
func Serialized(b PlaybackBackend) PlaybackBackend {
	if _, ok := b.(*serialized); ok {
		return b
	}
	return &serialized{b: b}
}

type serialized struct {
	mu sync.Mutex
	b  PlaybackBackend
}

func (s *serialized) Enqueue(urls ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Enqueue(urls...)
}

func (s *serialized) Insert(at int, urls ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Insert(at, urls...)
}

func (s *serialized) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Clear()
}

func (s *serialized) Remove(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Remove(index)
}

func (s *serialized) PlayAlbum(urls []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.PlayAlbum(urls)
}

func (s *serialized) Play() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Play()
}

func (s *serialized) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Pause()
}

func (s *serialized) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Stop()
}

func (s *serialized) Next() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Next()
}

func (s *serialized) Previous() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Previous()
}

func (s *serialized) Goto(index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Goto(index)
}

func (s *serialized) Seek(sec float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Seek(sec)
}

func (s *serialized) SeekRelative(sec float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.SeekRelative(sec)
}

func (s *serialized) SetVolume(v float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.SetVolume(v)
}

func (s *serialized) MoveItems(from, count, dst int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.MoveItems(from, count, dst)
}

func (s *serialized) Status() (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Status()
}

// IsLocalBackend forwards the on-device marker (see localMarker).
func (s *serialized) IsLocalBackend() bool { return IsLocal(s.b) }

// SetPlayListener forwards the local engine's play-log notification when the
// wrapped backend supports it, so the App's PlayNotifier hook keeps working
// through the wrapper.
func (s *serialized) SetPlayListener(fn func()) {
	if n, ok := s.b.(PlayNotifier); ok {
		n.SetPlayListener(fn)
	}
}

// Close closes the wrapped backend (it is an io.Closer in both concrete
// implementations); after Close the wrapper must not be used.
func (s *serialized) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.b.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
