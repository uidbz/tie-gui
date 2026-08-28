// Package playback defines the audio playback abstraction used by the UI and
// its concrete backends. The remote-first backend (pwplayRemote) drives a
// pwplay-server over HTTP; a local libmpv backend is planned behind the same
// interface (see the plan's Phase 6).
package playback

// Status is a backend-agnostic snapshot of the player, polled by the transport
// bar. It mirrors the fields pwplay reports.
type Status struct {
	Playing       bool
	Paused        bool
	Stopped       bool
	CurrentTrack  int      // index into Playlist, -1 when nothing is loaded
	CurrentFile   string   // backend's identifier for the current track
	Playlist      []string // ordered queue entries (URLs for the remote backend)
	TotalTracks   int
	Position      float64 // seconds into the current track
	TrackDuration float64 // seconds, 0 when unknown
	Volume        float64 // 0.0 silent … 1.0 default … 2.0 max
}

// PlaybackBackend controls a queue-based audio player. Implementations are not
// required to be safe for concurrent use; the UI calls them from background
// goroutines one action at a time.
type PlaybackBackend interface {
	// Enqueue appends tracks (stream URLs for the remote backend) to the queue.
	Enqueue(urls ...string) error
	// Insert adds tracks so the first lands at index `at` in the queue (clamped
	// to [0, len]); it appends when `at` is at or past the end.
	Insert(at int, urls ...string) error
	// Clear removes every track from the queue.
	Clear() error
	// PlayAlbum replaces the whole queue with urls and starts playback.
	PlayAlbum(urls []string) error

	Play() error
	Pause() error
	Stop() error
	Next() error
	Previous() error
	// Goto jumps to and plays the queue item at index.
	Goto(index int) error

	// Seek moves to an absolute position in seconds within the current track.
	Seek(sec float64) error
	// SeekRelative moves by an offset in seconds (negative rewinds).
	SeekRelative(sec float64) error
	// SetVolume sets the volume (0.0 silent, 1.0 default, 2.0 max).
	SetVolume(v float64) error

	// MoveItems relocates the queue range [from, from+count) to start at dst,
	// where dst is measured against the queue before the items are removed.
	MoveItems(from, count, dst int) error

	// Status returns a current snapshot for the transport bar.
	Status() (Status, error)
}
