package ui

import (
	"bytes"
	"image"
	"image/png"
	"sync"

	"fyne.io/fyne/v2"
	mobiledriver "fyne.io/fyne/v2/driver/mobile"

	"github.com/disintegration/imaging"
)

// mediaBridge feeds the Android media session and foreground playback
// service through the fyne driver: the lock screen, the playback
// notification, Bluetooth AVRCP and headset buttons all reach the player
// through here, as do audio-focus changes (calls, other apps) and the
// headphone-unplug broadcast.
//
// The bridge is a permanent transportView, fed by the player's regular
// status poll like every other view. It is active only while enabled (the
// local backend is playing) and the driver supports media sessions (Android);
// on the desktop the type assertion fails and the bridge inerts. Audio-focus
// policy lives here (in testable Go), not in the Java service.
type mediaBridge struct {
	player  *player
	driver  mobiledriver.MediaSessionDriver // nil when unsupported
	enabled bool

	mu       sync.Mutex
	started  bool // the service has been started and not torn down since
	sentMeta mobiledriver.MediaMetadata
	sentPlay bool
	sentPos  int64
	sentDur  int64
	cover    []byte // PNG artwork for the current track (nil = none yet)
	coverGen int

	pausedByFocus bool
	ducked        bool
	duckRestore   float64
}

// newMediaBridge builds the bridge and registers its action handler with the
// driver when the platform supports media sessions.
func newMediaBridge(p *player) *mediaBridge {
	b := &mediaBridge{player: p}
	if fyne.CurrentApp() == nil {
		return b
	}
	if d, ok := fyne.CurrentApp().Driver().(mobiledriver.MediaSessionDriver); ok {
		b.driver = d
		d.SetMediaActionHandler(b.handleAction)
	}
	return b
}

// SetEnabled turns the bridge on (local backend) or off. Disabling tears
// down the service: lock-screen controls must never drive a remote
// pwplay-server from the phone.
func (b *mediaBridge) SetEnabled(on bool) {
	b.mu.Lock()
	b.enabled = on
	b.mu.Unlock()
	if !on {
		b.stopService()
	}
}

// Stop tears the service down (app exit); safe to call repeatedly.
func (b *mediaBridge) Stop() {
	b.stopService()
}

func (b *mediaBridge) stopService() {
	b.mu.Lock()
	started := b.started
	b.started = false
	d := b.driver
	b.mu.Unlock()
	if started && d != nil {
		d.MediaSessionStop()
	}
}

// apply is the transportView feed: pushes change-only updates to the session.
// Runs on the UI goroutine.
func (b *mediaBridge) apply(st transportState) {
	b.mu.Lock()
	d := b.driver
	if !b.enabled || d == nil {
		b.mu.Unlock()
		return
	}

	// Nothing loaded and nothing playing: tear the service down (once) so no
	// orphaned notification outlives the queue.
	if !st.hasTrack && !st.playing {
		started := b.started
		b.started = false
		b.mu.Unlock()
		if started {
			d.MediaSessionStop()
		}
		return
	}

	meta := mobiledriver.MediaMetadata{Title: st.title, Artist: st.artist, Album: st.album}
	posMs := int64(st.position * 1000)
	durMs := int64(st.duration * 1000)
	// (ArtworkPNG is not part of the diff: it is pushed by setCover.)
	metaChanged := meta.Title != b.sentMeta.Title || meta.Artist != b.sentMeta.Artist ||
		meta.Album != b.sentMeta.Album
	playChanged := st.playing != b.sentPlay
	durChanged := durMs != b.sentDur
	// Between pushes the lock screen interpolates the position itself; a
	// drift beyond a few seconds means a seek happened (or playback is far
	// ahead of the poll), so re-anchor it.
	posDrift := posMs-b.sentPos > 4000 || b.sentPos-posMs > 4000
	cover := b.cover
	b.mu.Unlock()

	switch {
	case metaChanged:
		// Clear the artwork first (nil would keep the previous track's) and
		// push it separately once setCover has encoded the new one.
		d.MediaSessionUpdate(&meta, mobiledriver.MediaState{Playing: st.playing, PositionMS: posMs, DurationMS: durMs})
		if cover != nil {
			meta.ArtworkPNG = cover
			d.MediaSessionUpdate(&meta, mobiledriver.MediaState{Playing: st.playing, PositionMS: posMs, DurationMS: durMs})
		}
	case playChanged || durChanged || posDrift:
		meta.ArtworkPNG = nil // keep
		d.MediaSessionUpdate(&meta, mobiledriver.MediaState{Playing: st.playing, PositionMS: posMs, DurationMS: durMs})
	default:
		return
	}

	b.mu.Lock()
	b.started = true
	b.sentMeta = meta
	b.sentPlay = st.playing
	b.sentPos = posMs
	b.sentDur = durMs
	b.mu.Unlock()
}

// setCover is the transportView artwork feed: encodes the cover to a
// notification-sized PNG off the UI goroutine and re-pushes the metadata so
// the artwork appears. Runs on the UI goroutine.
func (b *mediaBridge) setCover(img image.Image) {
	b.mu.Lock()
	d := b.driver
	if !b.enabled || d == nil {
		b.mu.Unlock()
		return
	}
	b.coverGen++
	gen := b.coverGen
	b.mu.Unlock()

	go func() {
		var data []byte
		if img != nil {
			scaled := imaging.Fit(img, 256, 256, imaging.Linear)
			var buf bytes.Buffer
			if err := png.Encode(&buf, scaled); err == nil {
				data = buf.Bytes()
			}
		}
		fyne.Do(func() {
			b.mu.Lock()
			if gen != b.coverGen {
				b.mu.Unlock()
				return // a newer track's artwork is on its way
			}
			b.cover = data
			d := b.driver
			started := b.started
			meta := b.sentMeta
			playing := b.sentPlay
			pos, dur := b.sentPos, b.sentDur
			b.mu.Unlock()
			if started && d != nil {
				meta.ArtworkPNG = data
				d.MediaSessionUpdate(&meta, mobiledriver.MediaState{Playing: playing, PositionMS: pos, DurationMS: dur})
			}
		})
	}()
}

// handleAction dispatches transport and audio events from the playback
// service. It runs on a service thread (not the UI goroutine); the player's
// backend actions are goroutine-safe (they spawn their own goroutines).
func (b *mediaBridge) handleAction(action mobiledriver.MediaAction, arg int64) {
	p := b.player
	switch action {
	case mobiledriver.MediaActionPlay:
		go func() { _ = p.be().Play() }()
	case mobiledriver.MediaActionPause:
		go func() { _ = p.be().Pause() }()
	case mobiledriver.MediaActionNext:
		go func() { _ = p.be().Next() }()
	case mobiledriver.MediaActionPrevious:
		go func() { _ = p.be().Previous() }()
	case mobiledriver.MediaActionStop:
		go func() { _ = p.be().Stop() }()
		b.stopService()
	case mobiledriver.MediaActionSeek:
		go func() { _ = p.be().Seek(float64(arg) / 1000) }()
	case mobiledriver.MediaActionFocusLossTransient:
		if p.Playing() {
			b.mu.Lock()
			b.pausedByFocus = true
			b.mu.Unlock()
			go func() { _ = p.be().Pause() }()
		}
	case mobiledriver.MediaActionFocusLoss:
		// Permanent loss (another app took over): pause without auto-resume.
		b.mu.Lock()
		b.pausedByFocus = false
		b.mu.Unlock()
		go func() { _ = p.be().Pause() }()
	case mobiledriver.MediaActionFocusDuck:
		if s, err := p.be().Status(); err == nil {
			b.mu.Lock()
			b.ducked = true
			b.duckRestore = s.Volume
			b.mu.Unlock()
			go func() { _ = p.be().SetVolume(s.Volume * 0.35) }()
		}
	case mobiledriver.MediaActionFocusGain:
		b.mu.Lock()
		ducked, restore := b.ducked, b.duckRestore
		resume := b.pausedByFocus
		b.ducked, b.pausedByFocus = false, false
		b.mu.Unlock()
		if ducked {
			go func() { _ = p.be().SetVolume(restore) }()
		}
		if resume {
			go func() { _ = p.be().Play() }()
		}
	case mobiledriver.MediaActionBecomingNoisy:
		go func() { _ = p.be().Pause() }()
	}
}

// compile-time check: the bridge is a transport view.
var _ transportView = (*mediaBridge)(nil)
