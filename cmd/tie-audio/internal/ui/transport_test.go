package ui

import (
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// fakeBackend records the control calls the transport makes, so the slider
// echo guards can be verified: a poll pushing server state into a slider must
// never come back out as a Seek or SetVolume. Each Seek clears pwplay's ring
// buffer, so an echo is audible (chopped playback twice a second), which is
// what makes this worth a test.
type fakeBackend struct {
	mu      sync.Mutex
	seeks   []float64
	volumes []float64
	status  playback.Status
}

func (f *fakeBackend) Enqueue(...string) error       { return nil }
func (f *fakeBackend) Insert(int, ...string) error   { return nil }
func (f *fakeBackend) Clear() error                  { return nil }
func (f *fakeBackend) Remove(int) error              { return nil }
func (f *fakeBackend) PlayAlbum([]string) error      { return nil }
func (f *fakeBackend) Play() error                   { return nil }
func (f *fakeBackend) Pause() error                  { return nil }
func (f *fakeBackend) Stop() error                   { return nil }
func (f *fakeBackend) Next() error                   { return nil }
func (f *fakeBackend) Previous() error               { return nil }
func (f *fakeBackend) Goto(int) error                { return nil }
func (f *fakeBackend) SeekRelative(float64) error    { return nil }
func (f *fakeBackend) MoveItems(int, int, int) error { return nil }

func (f *fakeBackend) Seek(sec float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seeks = append(f.seeks, sec)
	return nil
}

func (f *fakeBackend) SetVolume(v float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes = append(f.volumes, v)
	return nil
}

func (f *fakeBackend) Status() (playback.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *fakeBackend) seekCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seeks)
}

func (f *fakeBackend) volumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.volumes)
}

// Every transport view owns its own sliders, so the guard has to hold across
// all of them at once: with three views registered, one poll performs three
// SetValue pairs, any of which could echo.
func TestPlayerApplyDoesNotEchoToBackend(t *testing.T) {
	test.NewApp()

	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	p.AddView(newRegularBar(p))
	p.AddView(newMiniBar(p, func() {}))
	p.AddView(newNowPlayingPage(p, func() {}))

	p.apply(playback.Status{
		Playing:       true,
		CurrentTrack:  0,
		Playlist:      []string{"http://host/hash"},
		TotalTracks:   1,
		Position:      12,
		TrackDuration: 300,
		Volume:        0.5,
	})

	if n := backend.seekCount(); n != 0 {
		t.Errorf("apply issued %d Seek calls, want 0", n)
	}
	if n := backend.volumeCount(); n != 0 {
		t.Errorf("apply issued %d SetVolume calls, want 0", n)
	}
}

// While the user drags a seek slider, a poll must not move the thumb out from
// under their finger.
func TestPlayerApplySkipsPositionWhileSeeking(t *testing.T) {
	test.NewApp()

	p := newPlayer(&fakeBackend{}, nil)
	bar := newRegularBar(p)
	p.AddView(bar)

	p.apply(playback.Status{CurrentTrack: -1, Position: 10, TrackDuration: 100})
	if got := bar.seek.Value; got != 10 {
		t.Fatalf("seek value = %v, want 10", got)
	}

	// Simulate a drag in progress: OnChanged fires per pointer move (marking
	// the player "seeking") and the thumb sits where the finger left it, with
	// no OnChangeEnded yet.
	bar.seek.OnChanged(42)
	bar.seek.Value = 42
	p.apply(playback.Status{CurrentTrack: -1, Position: 11, TrackDuration: 100})
	if got := bar.seek.Value; got != 42 {
		t.Errorf("seek value = %v, want the dragged 42 to be preserved", got)
	}
}

func TestPlayerNowPlayingLabels(t *testing.T) {
	p := newPlayer(&fakeBackend{}, nil)
	p.SetQueue([]string{"u1"}, []data.Track{
		{Hash: "h1", Title: "Song", Artist: "Artist", Album: "Album", AlbumUID: "A"},
	})

	got := p.nowPlaying(playback.Status{CurrentTrack: 0, Playlist: []string{"u1"}, TotalTracks: 1})
	if got.title != "Song" {
		t.Errorf("title = %q, want %q", got.title, "Song")
	}
	if want := "Artist · Album"; got.subtitle != want {
		t.Errorf("subtitle = %q, want %q", got.subtitle, want)
	}
	if got.albumUID != "A" {
		t.Errorf("albumUID = %q, want %q", got.albumUID, "A")
	}
	if !got.known {
		t.Error("known = false, want true for a registered track")
	}

	// An empty queue reads as idle, not as a blank track.
	idle := p.nowPlaying(playback.Status{CurrentTrack: -1})
	if idle.title != "Nothing playing" || idle.known {
		t.Errorf("idle = %+v, want the placeholder title and known=false", idle)
	}
}

// A track the registry does not know yet must not be recorded as the cover's
// URL, or the cover would never load once its metadata arrives.
func TestPlayerSyncCoverRetriesUnknownTrack(t *testing.T) {
	p := newPlayer(&fakeBackend{}, nil)

	p.syncCover(trackInfo{url: "u1", known: false})
	if p.coverURL != "" {
		t.Errorf("coverURL = %q after an unresolved track, want empty so the next poll retries", p.coverURL)
	}

	p.syncCover(trackInfo{url: "u1", known: true, albumUID: ""})
	if p.coverURL != "u1" {
		t.Errorf("coverURL = %q after a resolved track, want %q", p.coverURL, "u1")
	}
}

// The mini bar's volume row is hidden by default and shown by
// setVolumeVisible (the compact playlist view turns it on); the slider tracks
// the polled volume like every other view.
func TestMiniBarVolumeRow(t *testing.T) {
	test.NewApp()

	p := newPlayer(&fakeBackend{}, nil)
	bar := newMiniBar(p, func() {})
	p.AddView(bar)

	if bar.volRow.Visible() {
		t.Error("volume row visible by default, want hidden")
	}
	bar.setVolumeVisible(true)
	if !bar.volRow.Visible() {
		t.Error("volume row not shown by setVolumeVisible(true)")
	}
	bar.setVolumeVisible(false)
	if bar.volRow.Visible() {
		t.Error("volume row not hidden by setVolumeVisible(false)")
	}

	p.apply(playback.Status{Volume: 0.4})
	if got := bar.volume.Value; got != 0.4 {
		t.Errorf("volume slider = %v, want 0.4 after a poll", got)
	}
}

// closableBackend tracks Stop/Close for the SetBackend swap test.
type closableBackend struct {
	fakeBackend
	mu     sync.Mutex
	stops  int
	closes int
}

func (b *closableBackend) Stop() error {
	b.mu.Lock()
	b.stops++
	b.mu.Unlock()
	return nil
}

func (b *closableBackend) Close() error {
	b.mu.Lock()
	b.closes++
	b.mu.Unlock()
	return nil
}

// SetBackend swaps the backend the poll loop and actions target, and stops
// and closes the old one (the local engine holds a sink and a goroutine).
func TestPlayerSetBackend(t *testing.T) {
	test.NewApp()
	old := &closableBackend{}
	replacement := &fakeBackend{}
	p := newPlayer(old, nil)

	p.SetBackend(replacement)
	if got := p.be(); got != playback.PlaybackBackend(replacement) {
		t.Fatalf("backend = %T, want the replacement", got)
	}
	waitForCond(t, "old backend stopped and closed", func() bool {
		old.mu.Lock()
		defer old.mu.Unlock()
		return old.stops == 1 && old.closes == 1
	})

	// Actions land on the new backend.
	p.volumeStep(0.1)
	waitForCond(t, "volume on new backend", func() bool { return replacement.volumeCount() == 1 })

	// Swapping to the same backend is a no-op (no stop/close).
	p.SetBackend(replacement)
	time.Sleep(50 * time.Millisecond)
	old.mu.Lock()
	defer old.mu.Unlock()
	if old.stops != 1 || old.closes != 1 {
		t.Errorf("same-backend swap touched the old backend (stops=%d closes=%d)", old.stops, old.closes)
	}
}
