package playback

import (
	"fmt"
	"slices"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
)

// settleTimeout bounds every wait for pwplay's decoder loop to apply a
// queued mutation. The pwplay HTTP client has no per-request timeout
// (upstream), so this is the only wall-clock guard on the settle loops;
// a single wedged request still blocks (serialized wrapper keeps the rest
// of the UI waiting on that one operation rather than corrupting it).
const settleTimeout = 10 * time.Second

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
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	expected := append(slices.Clone(s.Playlist), urls...)
	if err := r.c.AddTracks(urls...); err != nil {
		return err
	}
	// AddTracks is applied asynchronously by pwplay's decoder loop (see
	// PlayAlbum), so a Status issued right after the POST can still show the
	// pre-add queue. Settle on the full CONTENT (not just the count, which
	// also matches while a different mutation is half-applied), so callers
	// refreshing the queue view immediately see the new tracks.
	return r.waitPlaylist(expected)
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
	expected := append(slices.Clone(s.Playlist), urls...)
	if err := r.c.AddTracks(urls...); err != nil {
		return err
	}
	if at == oldCount {
		return r.waitPlaylist(expected) // appended: settle, nothing to move
	}
	// AddTracks is applied asynchronously by pwplay's decoder loop, so the
	// appended block is not addressable until the applied playlist reflects
	// the add. Settle on CONTENT, not the count: the target count also
	// matches transiently mid-mutation (old + adds − removes), which would
	// send MoveItems against the wrong tail. On timeout, fail rather than
	// move blindly — the poll reconciles the queue view.
	if err := r.waitPlaylist(expected); err != nil {
		return fmt.Errorf("insert: %w", err)
	}
	// dst is measured against the queue before the block is removed; since the
	// appended block sits after `at`, dst == at.
	return r.c.MoveItems(oldCount, len(urls), at)
}

// Clear empties the queue. Servers with the /clear endpoint drain the whole
// playlist in one decoder-loop step (instant, and stops playback even when
// nothing was ever loaded); older servers fall back to stopping playback and
// removing the front oldCount times. Both paths settle on the observably
// empty queue before returning, so a queue view refreshed right after Clear
// does not re-show the old queue.
func (r *pwplayRemote) Clear() error {
	if err := r.c.ClearTracks(); err == nil {
		return r.waitPlaylist(nil)
	}
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	// Stop first: removing the *current* track while playing makes the
	// decoder reload whatever now occupies the current index — a full
	// download of each old track in turn, the churn PlayAlbum's comment
	// forbids.
	if err := r.c.Stop(); err != nil {
		return err
	}
	for i := 0; i < s.TotalTracks; i++ {
		if err := r.c.RemoveTrack(0); err != nil {
			return err
		}
	}
	return r.waitPlaylist(nil)
}

// Remove drops the queue item at index. pwplay applies the removal in its
// decoder loop, so the caller's next status poll reflects it.
func (r *pwplayRemote) Remove(index int) error {
	if index < 0 {
		return nil
	}
	return r.c.RemoveTrack(index)
}

// PlayAlbum replaces the queue then plays. The server applies every playlist
// mutation asynchronously in its decoder loop, so Play must not fire until the
// replace has actually landed: a Play racing the mutations is swallowed (the
// stale EOF decoder re-sets the stopped flag) and nothing plays until a second
// Play. It also must not trim the old queue one track at a time while playing:
// removing the *current* track makes the loop reload whatever now occupies the
// current index — a full download of each old track in turn — before the next
// removal lands, so the player churns through the whole old queue before
// reaching the new album.
//
// Servers with /clear (ClearTracks) get the clean path: one atomic clear, wait
// for the empty queue, add the new tracks, wait for exactly them, then Play.
// The clear is only sent when the queue is actually non-empty: the settle wait
// polls the applied playlist, and an already-empty queue matches instantly —
// the queued clear could still land *after* the adds and wipe the new album.
// Older servers (or an empty queue, where there is nothing to clear) fall back
// to append-then-trim (never transiently empty mid-replace, which would flip
// the player to stopped and race Play), but still wait for the queue to
// *equal* the new tracks — the final length alone matches transiently
// mid-mutation (old + adds − removes can hit len(urls) early), which is what
// let the old implementation Play into a half-replaced queue.
func (r *pwplayRemote) PlayAlbum(urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	s, err := r.c.Status()
	if err != nil {
		return err
	}
	oldCount := s.TotalTracks
	if oldCount > 0 {
		if err := r.c.ClearTracks(); err == nil {
			if err := r.waitPlaylist(nil); err != nil { // wait for the empty queue
				return err
			}
			if err := r.c.AddTracks(urls...); err != nil {
				return err
			}
			if err := r.waitPlaylist(urls); err != nil {
				return err
			}
			return r.c.Play()
		}
	}
	// Stop first: trimming while playing makes the decoder reload the
	// current index after every removal (downloading each old track in
	// turn); the Play below deliberately restarts on the new album.
	if err := r.c.Stop(); err != nil {
		return err
	}
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
	// actually settled to just the new tracks. Poll until the applied playlist
	// equals urls (content, not just length — see the doc comment), with a
	// bounded wait; if it never settles, report the failure instead of firing
	// Play into a half-replaced queue.
	if err := r.waitPlaylist(urls); err != nil {
		return err
	}
	return r.c.Play()
}

// waitPlaylist polls until the applied playlist is exactly want — i.e. every
// queued mutation has been applied by the decoder loop — giving up after
// settleTimeout. Matching the content, not just the length, makes the wait
// unambiguous mid-mutation. want == nil waits for the empty queue. A timeout
// is an error: callers must not Play or MoveItems against an unsettled queue.
func (r *pwplayRemote) waitPlaylist(want []string) error {
	deadline := time.Now().Add(settleTimeout)
	for {
		st, err := r.c.Status()
		if err != nil {
			return err
		}
		if playlistEquals(st.Playlist, want) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pwplay queue did not settle within %v (applied %d tracks, waiting for %d)",
				settleTimeout, len(st.Playlist), len(want))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func playlistEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
