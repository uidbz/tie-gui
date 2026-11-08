package ui

import (
	"image"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/uidbz/tie/client"

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
	nexts   int
	stops   int
	gotos   []int
	status  playback.Status
}

func (f *fakeBackend) Enqueue(...string) error       { return nil }
func (f *fakeBackend) Insert(int, ...string) error   { return nil }
func (f *fakeBackend) Clear() error                  { return nil }
func (f *fakeBackend) Remove(int) error              { return nil }
func (f *fakeBackend) PlayAlbum([]string) error      { return nil }
func (f *fakeBackend) Play() error                   { return nil }
func (f *fakeBackend) Pause() error                  { return nil }
func (f *fakeBackend) Previous() error               { return nil }
func (f *fakeBackend) SeekRelative(float64) error    { return nil }
func (f *fakeBackend) MoveItems(int, int, int) error { return nil }

func (f *fakeBackend) Stop() error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	return nil
}

func (f *fakeBackend) Next() error {
	f.mu.Lock()
	f.nexts++
	f.mu.Unlock()
	return nil
}

func (f *fakeBackend) Goto(i int) error {
	f.mu.Lock()
	f.gotos = append(f.gotos, i)
	f.mu.Unlock()
	return nil
}

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
	newTestApp(t)

	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	p.AddView(newRegularBar(p, nil))
	p.AddView(newMiniBar(p, func() {}, func() {}))
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
	newTestApp(t)

	p := newPlayer(&fakeBackend{}, nil)
	bar := newRegularBar(p, nil)
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

// The mini bar's volume row is always visible, and the slider tracks the
// polled volume like every other view. The track line appears only while a
// track is current.
func TestMiniBarVolumeRowAndTitle(t *testing.T) {
	newTestApp(t)

	p := newPlayer(&fakeBackend{}, nil)
	bar := newMiniBar(p, func() {}, func() {})
	p.AddView(bar)

	if !bar.volRow.Visible() {
		t.Error("volume row hidden, want always visible")
	}
	if bar.title.Visible() {
		t.Error("track line visible with nothing playing")
	}

	p.SetQueue([]string{"http://h/abc"}, []data.Track{{Title: "Song", Artist: "Band"}})
	// Gain 0.125 is level 0.5 on the cubic volume curve.
	p.apply(playback.Status{Volume: 0.125, Playlist: []string{"http://h/abc"}, CurrentTrack: 0, TotalTracks: 1})
	if got := bar.volume.Value; math.Abs(got-0.5) > 1e-9 {
		t.Errorf("volume slider = %v, want 0.5 after a poll", got)
	}
	if !bar.title.Visible() || bar.title.Text != "Song · Band" {
		t.Errorf("track line = %q (visible %v), want %q", bar.title.Text, bar.title.Visible(), "Song · Band")
	}
}

// transportState carries the queue position and the current track's hash
// (nav badge, desktop "3 / 42", album-view play marker).
func TestTransportStateQueueFields(t *testing.T) {
	newTestApp(t)
	p := newPlayer(&fakeBackend{}, nil)
	var got transportState
	p.AddView(recordView{&got})
	p.apply(playback.Status{Playlist: []string{"http://h/a", "http://h/b"}, CurrentTrack: 1, TotalTracks: 2})
	if got.queueLen != 2 || got.queueIndex != 1 || got.trackHash != "b" {
		t.Errorf("state = len %d idx %d hash %q, want 2, 1, %q", got.queueLen, got.queueIndex, got.trackHash, "b")
	}
	if s := queuePosition(got); s != "2 / 2" {
		t.Errorf("queuePosition = %q, want %q", s, "2 / 2")
	}
	if s := queuePosition(transportState{queueLen: 5, queueIndex: -1}); s != "5 tracks" {
		t.Errorf("queuePosition(no current) = %q, want %q", s, "5 tracks")
	}
	if s := queuePosition(transportState{queueIndex: -1}); s != "" {
		t.Errorf("queuePosition(empty) = %q, want empty", s)
	}
}

// Cue tracks must reach the queue as distinct URLs (the filehost ignores the
// fragment), or every logical track would share one registry entry.
func TestStreamableUsesCueKey(t *testing.T) {
	cfg := client.DefaultConfig()
	cfg.FileHosts = map[string]client.FileHost{"default": {URL: "http://host:1162"}}
	cfg.DefaultFileHosts = []string{"default"}
	b := &browsePage{}
	b.session.Store(&data.Session{Tie: client.NewTieClient(cfg)})

	urls, meta := b.streamable([]data.Track{{
		Hash: "img", Title: "Track", Cue: &data.CueSegment{Start: 10, End: 20},
	}})
	if len(urls) != 1 || len(meta) != 1 {
		t.Fatalf("streamable = %v / %v", urls, meta)
	}
	if want := "http://host:1162/img#cue=10000-20000"; urls[0] != want {
		t.Errorf("url = %q, want %q", urls[0], want)
	}
}

// A cue track's transport clock is segment-relative: the backend reports the
// whole image's position/duration.
func TestSegmentClock(t *testing.T) {
	plain := trackInfo{}
	pos, dur := segmentClock(playback.Status{Position: 12, TrackDuration: 100}, plain)
	if pos != 12 || dur != 100 {
		t.Errorf("plain clock = (%v, %v)", pos, dur)
	}

	cue := trackInfo{cue: &data.CueSegment{Start: 30, End: 60}}
	pos, dur = segmentClock(playback.Status{Position: 42, TrackDuration: 200}, cue)
	if pos != 12 || dur != 30 {
		t.Errorf("cue clock = (%v, %v), want (12, 30)", pos, dur)
	}

	// An open-ended segment runs to the image's end.
	open := trackInfo{cue: &data.CueSegment{Start: 30}}
	pos, dur = segmentClock(playback.Status{Position: 50, TrackDuration: 200}, open)
	if pos != 20 || dur != 170 {
		t.Errorf("open cue clock = (%v, %v), want (20, 170)", pos, dur)
	}
}

// Seeking a cue track targets the segment-relative slider value plus the
// segment's start in the image.
func TestPlayerSeekToTranslatesCueSegment(t *testing.T) {
	newTestApp(t)
	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	url := "http://h/img#cue=30000-60000"
	p.SetQueue([]string{url}, []data.Track{{Hash: "img", Cue: &data.CueSegment{Start: 30, End: 60}}})
	p.apply(playback.Status{CurrentTrack: 0, Playlist: []string{url}, TotalTracks: 1, TrackDuration: 200})

	p.seekTo(12)
	waitForCond(t, "seek translated to the image position", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return len(backend.seeks) == 1 && math.Abs(backend.seeks[0]-42) < 1e-9
	})
}

// A cue segment starts where INDEX 01 says (the backend loads at 0) and ends
// at its boundary; the first poll after the boundary advances once, not on
// every subsequent poll.
func TestPlayerEnforceCueSegment(t *testing.T) {
	newTestApp(t)
	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	u1 := "http://h/img#cue=10000-20000"
	u2 := "http://h/next"
	p.SetQueue([]string{u1, u2}, []data.Track{
		{Hash: "img", Cue: &data.CueSegment{Start: 10, End: 20}},
		{Hash: "next"},
	})

	p.apply(playback.Status{Playing: true, CurrentTrack: 0, Playlist: []string{u1, u2}, TotalTracks: 2, Position: 0, TrackDuration: 100})
	waitForCond(t, "start seek into the image", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return len(backend.seeks) == 1 && backend.seeks[0] == 10
	})

	end := playback.Status{Playing: true, CurrentTrack: 0, Playlist: []string{u1, u2}, TotalTracks: 2, Position: 20, TrackDuration: 100}
	p.apply(end)
	p.apply(end) // the repeated poll must not skip two tracks
	waitForCond(t, "one advance at the segment end", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.nexts == 1
	})
}

// The last cue segment stops at its boundary instead of playing to the end of
// the image; repeat-all restarts the queue.
func TestPlayerEnforceCueLastSegment(t *testing.T) {
	newTestApp(t)
	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	url := "http://h/img#cue=10000-20000"
	p.SetQueue([]string{url}, []data.Track{{Hash: "img", Cue: &data.CueSegment{Start: 10, End: 20}}})
	p.apply(playback.Status{Playing: true, CurrentTrack: 0, Playlist: []string{url}, TotalTracks: 1, Position: 20, TrackDuration: 100})
	waitForCond(t, "stop at the last segment end", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.stops == 1
	})

	p.SetRepeat(true)
	p.cueAdvanced = false
	p.apply(playback.Status{Playing: true, CurrentTrack: 0, Playlist: []string{url}, TotalTracks: 1, Position: 20, TrackDuration: 100})
	waitForCond(t, "repeat restarts the queue", func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return len(backend.gotos) == 1 && backend.gotos[0] == 0
	})
}

// recordView captures the last applied transportState.
type recordView struct{ st *transportState }

func (r recordView) apply(st transportState) { *r.st = st }
func (r recordView) setCover(image.Image)    {}

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
	newTestApp(t)
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
	sleepUI(50 * time.Millisecond)
	old.mu.Lock()
	defer old.mu.Unlock()
	if old.stops != 1 || old.closes != 1 {
		t.Errorf("same-backend swap touched the old backend (stops=%d closes=%d)", old.stops, old.closes)
	}
}

// The MPRIS model: stopped vs paused, next/previous availability, and no
// track metadata while nothing is current.
func TestMPRISState(t *testing.T) {
	st := transportState{playing: false, hasTrack: true, title: "Song", artist: "Band",
		trackHash: "h", queueLen: 3, queueIndex: 2, duration: 100, position: 5, volume: 1}
	ms := mprisState(st, true, "file:///x.png")
	if ms.Stopped || ms.Playing || !ms.HasTrack || ms.Title != "Song" || ms.ArtURL != "file:///x.png" {
		t.Errorf("paused state = %+v", ms)
	}
	if ms.CanNext || !ms.CanPrev || !ms.RepeatAll {
		t.Errorf("last track: CanNext %v CanPrev %v RepeatAll %v", ms.CanNext, ms.CanPrev, ms.RepeatAll)
	}
	st.stopped = true
	if !mprisState(st, false, "").Stopped {
		t.Error("stopped player not reported Stopped")
	}
	none := mprisState(transportState{title: "Nothing playing", queueIndex: -1}, false, "file:///x.png")
	if !none.Stopped || none.Title != "" || none.ArtURL != "" {
		t.Errorf("no track state = %+v", none)
	}
}

// The volume controls work in perceptual levels (0…1, cubic like PipeWire)
// and send linear gain to the backend, never above unity: the old linear
// 0…2 gain slider was far too loud over most of its range.
func TestVolumeLevelsMapToCubicGain(t *testing.T) {
	newTestApp(t)

	fb := &fakeBackend{}
	p := newPlayer(fb, nil)
	s := p.newVolumeSlider()
	if s.Min != 0 || s.Max != 1 {
		t.Fatalf("volume slider range = %v…%v, want 0…1", s.Min, s.Max)
	}

	s.OnChangeEnded(0.5)
	waitVolumes(t, fb, 1)
	if got := fb.lastVolume(); math.Abs(got-0.125) > 1e-9 {
		t.Errorf("slider at 50%% sent gain %v, want 0.125", got)
	}

	// A poll at unity gain shows 100 %; stepping up stays at unity.
	p.apply(playback.Status{Volume: 1})
	p.volumeStep(0.1)
	waitVolumes(t, fb, 2)
	if got := fb.lastVolume(); got != 1 {
		t.Errorf("volume step above 100%% sent gain %v, want 1", got)
	}

	// A boosted gain (set by another client) reads as 100 %, and a step down
	// lands at level 0.9.
	p.apply(playback.Status{Volume: 2})
	if got := p.currentVolume(); got != 1 {
		t.Errorf("level for gain 2 = %v, want 1", got)
	}
	p.volumeStep(-0.1)
	waitVolumes(t, fb, 3)
	if got := fb.lastVolume(); math.Abs(got-0.729) > 1e-9 {
		t.Errorf("step down from 100%% sent gain %v, want 0.729", got)
	}
}

func (f *fakeBackend) lastVolume() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.volumes[len(f.volumes)-1]
}

func waitVolumes(t *testing.T, f *fakeBackend, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for f.volumeCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("got %d SetVolume calls, want %d", f.volumeCount(), n)
		}
		sleepUI(time.Millisecond)
	}
}
