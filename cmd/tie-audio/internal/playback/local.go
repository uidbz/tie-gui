package playback

import (
	"time"

	pwplayer "github.com/uidbz/pwplay/player"
)

// localBackend plays on this device using pwplay's player engine — the same
// queue/gapless/seek engine pwplay-server runs, driven directly instead of
// over HTTP. Queue entries are the same stream URLs the remote backend uses;
// the engine downloads each track to a temp file on load and picks a decoder
// from the server-reported Content-Type.
//
// The output sink is the engine's platform default (PipeWire on Linux,
// OpenSL ES on Android); tests inject a fake sink via newLocalWithSink.
type localBackend struct {
	p *pwplayer.Player
}

// NewLocal builds the on-device backend. Construction cannot fail on Android
// (the sink is created lazily at the first track load); on Linux a missing
// PipeWire library surfaces at load time, logged by the engine.
func NewLocal() (PlaybackBackend, error) {
	return newLocalWithSink(nil)
}

// IsLocal reports whether b is the on-device backend — used to decide
// whether phone-side integration (media session, audio focus) applies.
func IsLocal(b PlaybackBackend) bool {
	_, ok := b.(*localBackend)
	return ok
}

// newLocalWithSink is NewLocal with a sink-factory seam for tests.
func newLocalWithSink(sink pwplayer.SinkFactory) (PlaybackBackend, error) {
	p, err := pwplayer.NewPlayerWithOptions(nil, pwplayer.PlayerOptions{
		StartPaused: true,
		Sink:        sink,
	})
	if err != nil {
		return nil, err
	}
	return &localBackend{p: p}, nil
}

// Close shuts the engine down (sink, decoder goroutine, temp files). The
// backend must not be used afterwards. Called via io.Closer assertion when
// the app exits or the backend is switched.
func (b *localBackend) Close() error {
	b.p.Close()
	return nil
}

// waitTotal polls (bounded) until the engine's playlist length equals want.
// Engine queue mutations are applied by its decoder loop between decode
// iterations, so a Status issued right after Enqueue/Remove could otherwise
// still show the pre-mutation queue — the UI refreshes immediately after
// these calls (mirroring pwplayRemote's settle waits, but in-process and
// therefore much faster).
func (b *localBackend) waitTotal(want int) {
	for i := 0; i < 200; i++ {
		if len(b.p.Playlist()) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (b *localBackend) Enqueue(urls ...string) error {
	if len(urls) == 0 {
		return nil
	}
	target := len(b.p.Playlist()) + len(urls)
	for _, u := range urls {
		b.p.AddTrack(u)
	}
	b.waitTotal(target)
	return nil
}

func (b *localBackend) Insert(at int, urls ...string) error {
	if len(urls) == 0 {
		return nil
	}
	old := len(b.p.Playlist())
	if at < 0 {
		at = 0
	}
	if at > old {
		at = old
	}
	for _, u := range urls {
		b.p.AddTrack(u)
	}
	if at == old {
		b.waitTotal(old + len(urls))
		return nil
	}
	// The moved block must be addressable before MoveItems validates it
	// against the engine's playlist.
	b.waitTotal(old + len(urls))
	// dst is measured against the queue before the block is removed; the
	// appended block sits after `at`, so dst == at.
	return b.p.MoveItems(old, len(urls), at)
}

func (b *localBackend) Clear() error {
	b.p.ClearTracks()
	b.waitTotal(0)
	return nil
}

func (b *localBackend) Remove(index int) error {
	old := len(b.p.Playlist())
	if index < 0 || index >= old {
		return nil
	}
	b.p.RemoveTrack(index)
	b.waitTotal(old - 1)
	return nil
}

// PlayAlbum replaces the queue then plays: clear (one decoder-loop step that
// also silences the ring immediately), wait for the empty queue, add the new
// tracks, wait for exactly them, then Play. Settling between phases is what
// makes the clear safe — a clear still queued when the adds land would empty
// the queue after them, and Play firing into that empty queue leaves the
// engine stopped. The clear is therefore only sent when the queue is actually
// non-empty: an already-empty queue matches the settle wait instantly, so a
// queued clear could still land after the adds and wipe the new album.
// Trimming the old queue one track at a time instead (the old approach) makes
// the loop reload — a full download of — each old track whose removal
// displaced the current index, churning through the old queue before the new
// album starts.
func (b *localBackend) PlayAlbum(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	if len(b.p.Playlist()) > 0 {
		b.p.ClearTracks()
		b.waitTotal(0)
	}
	for _, u := range urls {
		b.p.AddTrack(u)
	}
	b.waitTotal(len(urls))
	b.p.Play()
	return nil
}

func (b *localBackend) Play() error      { b.p.Play(); return nil }
func (b *localBackend) Pause() error     { b.p.Pause(); return nil }
func (b *localBackend) Stop() error      { b.p.Stop(); return nil }
func (b *localBackend) Next() error      { b.p.Next(); return nil }
func (b *localBackend) Previous() error  { b.p.Previous(); return nil }
func (b *localBackend) Goto(i int) error { b.p.Goto(i); return nil }

func (b *localBackend) Seek(sec float64) error       { b.p.Seek(sec); return nil }
func (b *localBackend) SeekRelative(s float64) error { b.p.SeekRelative(s); return nil }
func (b *localBackend) SetVolume(v float64) error    { b.p.SetVolume(v); return nil }
func (b *localBackend) MoveItems(f, c, d int) error  { return b.p.MoveItems(f, c, d) }

func (b *localBackend) Status() (Status, error) {
	playlist := b.p.Playlist()
	current := b.p.CurrentTrack()
	if len(playlist) == 0 {
		// Status contract: -1 when nothing is loaded.
		current = -1
	} else if current >= len(playlist) {
		current = len(playlist) - 1
	}
	duration := b.p.TrackDuration()
	if duration < 0 {
		duration = 0 // Status contract: 0 when unknown
	}
	return Status{
		Playing:       b.p.IsPlaying(),
		Paused:        b.p.IsPaused(),
		Stopped:       b.p.IsStopped(),
		CurrentTrack:  current,
		CurrentFile:   b.p.CurrentFile(),
		Playlist:      playlist,
		TotalTracks:   len(playlist),
		Position:      b.p.Position(),
		TrackDuration: duration,
		Volume:        b.p.Volume(),
	}, nil
}
