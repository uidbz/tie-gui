package playback

import (
	"time"

	pwclient "github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/pwplay/client"
)

// pwplayRemote drives a pwplay-server over HTTP, wrapping pwplay/client. It has
// no cgo dependency, so it works on Android as well as desktop.
type pwplayRemote struct {
	c *pwclient.Client
}

// NewPwplayRemote builds a remote backend over the given pwplay client.
func NewPwplayRemote(c *pwclient.Client) PlaybackBackend {
	return &pwplayRemote{c: c}
}

func (r *pwplayRemote) Enqueue(urls ...string) error {
	if len(urls) == 0 {
		return nil
	}
	return r.c.AddTracks(urls...)
}

// Insert adds urls so the first lands at index `at` in the queue. pwplay has no
// insert call, so it appends the tracks (they land at the back), waits for the
// async add to settle so the appended block is addressable, then relocates that
// block to `at` with a single MoveItems. Appending (at >= len) skips the move.
func (r *pwplayRemote) Insert(at int, urls ...string) error {
	if len(urls) == 0 {
		return nil
	}
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	oldCount := s.TotalTracks
	if at < 0 {
		at = 0
	}
	if at > oldCount {
		at = oldCount
	}
	if err := r.c.AddTracks(urls...); err != nil {
		return err
	}
	if at == oldCount {
		return nil // already at the end
	}
	// AddTracks is applied asynchronously by pwplay's decoder loop, so the moved
	// block is not addressable until TotalTracks reflects the add. Poll (bounded)
	// before MoveItems, mirroring PlayAlbum.
	target := oldCount + len(urls)
	for i := 0; i < 40; i++ {
		st, err := r.c.Status()
		if err != nil {
			return err
		}
		if st.TotalTracks == target {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	// dst is measured against the queue before the block is removed; since the
	// appended block sits after `at`, dst == at.
	return r.c.MoveItems(oldCount, len(urls), at)
}

// Clear empties the queue by removing index 0 repeatedly (pwplay has no
// clear-playlist call). Removals are applied asynchronously; removing the front
// oldCount times drains exactly the tracks present when Clear was called.
func (r *pwplayRemote) Clear() error {
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	for i := 0; i < s.TotalTracks; i++ {
		if err := r.c.RemoveTrack(0); err != nil {
			return err
		}
	}
	return nil
}

// PlayAlbum replaces the queue then plays. pwplay has no clear-playlist call
// and processes add/remove asynchronously, so the order matters: we APPEND the
// new tracks first, then trim exactly the old tracks off the front (index 0),
// then Play. Because the new tracks stay at the back, the queue is never
// transiently empty — which would flip the player to a stopped state and race
// against Play — and trimming the recorded old count removes exactly the old
// tracks regardless of how the async add/remove messages interleave.
func (r *pwplayRemote) PlayAlbum(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	oldCount := s.TotalTracks
	if err := r.c.AddTracks(urls...); err != nil {
		return err
	}
	for i := 0; i < oldCount; i++ {
		if err := r.c.RemoveTrack(0); err != nil {
			return err
		}
	}
	// AddTracks/RemoveTrack are applied asynchronously by pwplay's decoder loop
	// (buffered command channels), so Play() must not fire until the queue has
	// actually settled to just the new tracks. Otherwise Play races the removal
	// of the old current track (e.g. the startup placeholder) and its currentTrack
	// reindex, and playback silently fails to start until a second Play. Poll the
	// applied playlist length (TotalTracks reflects the loop-applied state) until
	// it matches, with a bounded wait so a lost/extra track can't hang us.
	for i := 0; i < 40; i++ {
		st, err := r.c.Status()
		if err != nil {
			return err
		}
		if st.TotalTracks == len(urls) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return r.c.Play()
}

func (r *pwplayRemote) Play() error                  { return r.c.Play() }
func (r *pwplayRemote) Pause() error                 { return r.c.Pause() }
func (r *pwplayRemote) Stop() error                  { return r.c.Stop() }
func (r *pwplayRemote) Next() error                  { return r.c.Next() }
func (r *pwplayRemote) Previous() error              { return r.c.Previous() }
func (r *pwplayRemote) Goto(index int) error         { return r.c.Goto(index) }
func (r *pwplayRemote) Seek(sec float64) error       { return r.c.Seek(sec) }
func (r *pwplayRemote) SeekRelative(s float64) error { return r.c.SeekRelative(s) }
func (r *pwplayRemote) SetVolume(v float64) error    { return r.c.SetVolume(v) }

func (r *pwplayRemote) MoveItems(from, count, dst int) error {
	return r.c.MoveItems(from, count, dst)
}

func (r *pwplayRemote) Status() (Status, error) {
	s, err := r.c.Status()
	if err != nil {
		return Status{}, err
	}
	return Status{
		Playing:       s.Playing,
		Paused:        s.Paused,
		Stopped:       s.Stopped,
		CurrentTrack:  s.CurrentTrack,
		CurrentFile:   s.CurrentFile,
		Playlist:      s.Playlist,
		TotalTracks:   s.TotalTracks,
		Position:      s.Position,
		TrackDuration: s.TrackDuration,
		Volume:        s.Volume,
	}, nil
}
