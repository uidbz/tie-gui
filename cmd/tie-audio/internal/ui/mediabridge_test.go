package ui

import (
	"image"
	"sync"
	"testing"

	mobiledriver "fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// fakeMediaDriver records the updates a mediaBridge sends and holds the
// registered action handler.
type fakeMediaDriver struct {
	mu      sync.Mutex
	handler func(mobiledriver.MediaAction, int64)
	updates []mediaUpdate
	stops   int
}

type mediaUpdate struct {
	meta  mobiledriver.MediaMetadata
	state mobiledriver.MediaState
}

func (d *fakeMediaDriver) SetMediaActionHandler(fn func(mobiledriver.MediaAction, int64)) {
	d.mu.Lock()
	d.handler = fn
	d.mu.Unlock()
}

func (d *fakeMediaDriver) MediaSessionUpdate(meta *mobiledriver.MediaMetadata, state mobiledriver.MediaState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.updates = append(d.updates, mediaUpdate{*meta, state})
}

func (d *fakeMediaDriver) MediaSessionStop() {
	d.mu.Lock()
	d.stops++
	d.mu.Unlock()
}

func (d *fakeMediaDriver) last() mediaUpdate {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.updates[len(d.updates)-1]
}

func (d *fakeMediaDriver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.updates)
}

// bridgeBackend records the transport calls the bridge dispatches.
type bridgeBackend struct {
	fakeBackend

	mu      sync.Mutex
	pauses  int
	nexts   int
	prevs   int
	stops   int
	plays   int
	playing bool
}

func (b *bridgeBackend) Pause() error {
	b.mu.Lock()
	b.pauses++
	b.playing = false
	b.mu.Unlock()
	return nil
}

func (b *bridgeBackend) Play() error {
	b.mu.Lock()
	b.plays++
	b.playing = true
	b.mu.Unlock()
	return nil
}

func (b *bridgeBackend) Stop() error {
	b.mu.Lock()
	b.stops++
	b.playing = false
	b.mu.Unlock()
	return nil
}

func (b *bridgeBackend) Next() error {
	b.mu.Lock()
	b.nexts++
	b.mu.Unlock()
	return nil
}

func (b *bridgeBackend) Previous() error {
	b.mu.Lock()
	b.prevs++
	b.mu.Unlock()
	return nil
}

func (b *bridgeBackend) Status() (playback.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return playback.Status{Playing: b.playing, Volume: 1, Playlist: []string{"u"}, CurrentTrack: 0}, nil
}

func newTestBridge(t *testing.T, be *bridgeBackend) (*mediaBridge, *fakeMediaDriver) {
	t.Helper()
	test.NewApp()
	p := newPlayer(be, nil)
	b := newMediaBridge(p)
	d := &fakeMediaDriver{}
	b.driver = d
	b.enabled = true
	return b, d
}

func TestMediaBridgePushesChanges(t *testing.T) {
	b, d := newTestBridge(t, &bridgeBackend{})

	b.apply(transportState{hasTrack: true, title: "Song", artist: "Artist", album: "Album", playing: true, position: 3, duration: 200})
	if d.count() != 1 {
		t.Fatalf("first state should push exactly once, got %d", d.count())
	}
	u := d.last()
	if u.meta.Title != "Song" || u.meta.Artist != "Artist" || u.meta.Album != "Album" {
		t.Errorf("meta = %+v", u.meta)
	}
	if !u.state.Playing || u.state.PositionMS != 3000 || u.state.DurationMS != 200000 {
		t.Errorf("state = %+v", u.state)
	}

	// Same state again: no push.
	b.apply(transportState{hasTrack: true, title: "Song", artist: "Artist", album: "Album", playing: true, position: 3.5, duration: 200})
	if d.count() != 1 {
		t.Fatalf("unchanged state pushed again (count %d)", d.count())
	}

	// Play/pause flip pushes.
	b.apply(transportState{hasTrack: true, title: "Song", artist: "Artist", album: "Album", position: 4, duration: 200})
	if d.count() != 2 {
		t.Fatalf("pause flip not pushed (count %d)", d.count())
	}
	if d.last().state.Playing {
		t.Error("last update should be paused")
	}

	// A seek (position far from the last pushed) re-anchors.
	b.apply(transportState{hasTrack: true, title: "Song", artist: "Artist", album: "Album", position: 120, duration: 200})
	if d.count() != 3 {
		t.Fatalf("seek drift not pushed (count %d)", d.count())
	}
	if d.last().state.PositionMS != 120000 {
		t.Errorf("seek anchor = %d, want 120000", d.last().state.PositionMS)
	}

	// Track change pushes new metadata.
	b.apply(transportState{hasTrack: true, title: "Next Song", artist: "Artist", album: "Album", playing: true, position: 0, duration: 180})
	if d.count() != 4 || d.last().meta.Title != "Next Song" {
		t.Fatalf("track change not pushed (count %d)", d.count())
	}

	// Empty queue + stopped tears the service down.
	b.apply(transportState{})
	d.mu.Lock()
	stops := d.stops
	d.mu.Unlock()
	if stops != 1 {
		t.Fatalf("empty stopped state should stop the service once (stops %d)", stops)
	}
}

func TestMediaBridgeDisabledInert(t *testing.T) {
	b, d := newTestBridge(t, &bridgeBackend{})
	b.SetEnabled(false)
	d.mu.Lock()
	if d.stops != 0 {
		t.Error("SetEnabled(false) stopped a service that never started")
	}
	d.mu.Unlock()
	b.apply(transportState{hasTrack: true, title: "Song", playing: true})
	if d.count() != 0 {
		t.Error("disabled bridge pushed updates")
	}
}

func TestMediaBridgeTransportActions(t *testing.T) {
	be := &bridgeBackend{}
	b, _ := newTestBridge(t, be)

	b.handleAction(mobiledriver.MediaActionPlay, 0)
	waitForCond(t, "play", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.plays == 1 })
	b.handleAction(mobiledriver.MediaActionPause, 0)
	waitForCond(t, "pause", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.pauses == 1 })
	b.handleAction(mobiledriver.MediaActionNext, 0)
	b.handleAction(mobiledriver.MediaActionPrevious, 0)
	waitForCond(t, "next/prev", func() bool {
		be.mu.Lock()
		defer be.mu.Unlock()
		return be.nexts == 1 && be.prevs == 1
	})
	b.handleAction(mobiledriver.MediaActionStop, 0)
	waitForCond(t, "stop", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.stops == 1 })
	b.handleAction(mobiledriver.MediaActionSeek, 61500)
	waitForCond(t, "seek", func() bool {
		be.fakeBackend.mu.Lock()
		defer be.fakeBackend.mu.Unlock()
		return len(be.fakeBackend.seeks) == 1 && be.fakeBackend.seeks[0] == 61.5
	})
}

func TestMediaBridgeFocusPolicy(t *testing.T) {
	be := &bridgeBackend{playing: true}
	b, _ := newTestBridge(t, be)
	// The bridge checks player.Playing() for the transient-loss policy.
	b.player.mu.Lock()
	b.player.playing = true
	b.player.mu.Unlock()

	// Transient loss pauses, and only if we were playing; gain resumes.
	b.handleAction(mobiledriver.MediaActionFocusLossTransient, 0)
	waitForCond(t, "transient pause", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.pauses == 1 })
	b.handleAction(mobiledriver.MediaActionFocusGain, 0)
	waitForCond(t, "transient resume", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.plays == 1 })

	// Duck lowers the volume, gain restores it.
	b.handleAction(mobiledriver.MediaActionFocusDuck, 0)
	waitForCond(t, "duck", func() bool {
		be.fakeBackend.mu.Lock()
		defer be.fakeBackend.mu.Unlock()
		return len(be.fakeBackend.volumes) == 1 && be.fakeBackend.volumes[0] < 0.5
	})
	b.handleAction(mobiledriver.MediaActionFocusGain, 0)
	waitForCond(t, "duck restore", func() bool {
		be.fakeBackend.mu.Lock()
		defer be.fakeBackend.mu.Unlock()
		return len(be.fakeBackend.volumes) == 2 && be.fakeBackend.volumes[1] == 1
	})

	// Permanent loss pauses without resume on the next gain (the gain handler
	// is fully synchronous when it has nothing to restore).
	b.handleAction(mobiledriver.MediaActionFocusLoss, 0)
	waitForCond(t, "loss pause", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.pauses == 2 })
	b.handleAction(mobiledriver.MediaActionFocusGain, 0)
	if got := func() int { be.mu.Lock(); defer be.mu.Unlock(); return be.plays }(); got != 1 {
		t.Errorf("focus gain after permanent loss resumed playback (plays %d)", got)
	}

	// Headphone unplug pauses.
	b.handleAction(mobiledriver.MediaActionBecomingNoisy, 0)
	waitForCond(t, "noisy pause", func() bool { be.mu.Lock(); defer be.mu.Unlock(); return be.pauses == 3 })
}

func TestMediaBridgeCover(t *testing.T) {
	b, d := newTestBridge(t, &bridgeBackend{})
	b.apply(transportState{hasTrack: true, title: "Song", playing: true, position: 1, duration: 10})
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	b.setCover(img)
	waitForCond(t, "cover pushed", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.updates) == 2 && len(d.updates[1].meta.ArtworkPNG) > 0
	})
}
