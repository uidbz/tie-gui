package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sync"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/mpris"
)

// mprisBridge publishes the player on the desktop session bus as an MPRIS 2
// media player (internal/mpris), so system-wide controls reach tie-audio:
// desktop media keys, `playerctl`, status-bar modules, and window-manager
// bindings (sway: `bindsym XF86AudioPlay exec playerctl -p tie_audio
// play-pause`, or `exec tie-audio -control play-pause` without playerctl).
//
// Like the Android media bridge it is a permanent transportView fed by the
// player's status poll; unlike it, it drives whichever backend is active
// (remote pwplay too) — the desktop controls the app, and the app controls
// the backend. Where there is no session bus (Android, other OSes, or a
// headless session) Start fails and the bridge stays inert.
type mprisBridge struct {
	player *player
	win    fyne.Window
	server *mpris.Server

	mu    sync.Mutex
	state mpris.State
	// artPath is the cover file currently advertised as mpris:artUrl (a new
	// file per cover, so clients that cache by URL re-read it); artGen drops
	// an encode that finished after the track moved on.
	artPath string
	artGen  int
}

// newMPRISBridge starts the MPRIS server. It returns a bridge whether or not
// the server could start; err reports why it is inert.
func newMPRISBridge(p *player, win fyne.Window) (*mprisBridge, error) {
	b := &mprisBridge{player: p, win: win}
	srv, err := mpris.Start(mprisController{b})
	if err != nil {
		return b, err
	}
	b.server = srv
	return b, nil
}

// Close releases the bus name and removes the cover file.
func (b *mprisBridge) Close() {
	if b == nil {
		return
	}
	if b.server != nil {
		_ = b.server.Close()
	}
	b.mu.Lock()
	if b.artPath != "" {
		_ = os.Remove(b.artPath)
		b.artPath = ""
	}
	b.mu.Unlock()
}

// mprisState converts a transport snapshot into the MPRIS model. artURL is
// carried over (setCover owns it).
func mprisState(st transportState, repeat bool, artURL string) mpris.State {
	ms := mpris.State{
		Playing:   st.playing,
		Stopped:   st.stopped || !st.hasTrack,
		HasTrack:  st.hasTrack,
		TrackID:   st.trackHash,
		Artist:    st.artist,
		Album:     st.album,
		Duration:  st.duration,
		Position:  st.position,
		Volume:    st.volume,
		ArtURL:    artURL,
		CanNext:   st.queueIndex >= 0 && st.queueIndex < st.queueLen-1,
		CanPrev:   st.queueLen > 0,
		RepeatAll: repeat,
	}
	if st.hasTrack {
		ms.Title = st.title
	}
	if !st.hasTrack {
		ms.ArtURL = ""
	}
	return ms
}

func (b *mprisBridge) apply(st transportState) {
	if b.server == nil {
		return
	}
	b.mu.Lock()
	b.state = mprisState(st, b.player.RepeatAll(), b.state.ArtURL)
	ms := b.state
	b.mu.Unlock()
	b.server.Update(ms)
}

// setCover writes the album art to a PNG under the runtime dir and
// advertises it as mpris:artUrl (notification daemons and status bars show
// it). Encoding runs off the UI goroutine.
func (b *mprisBridge) setCover(img image.Image) {
	if b.server == nil {
		return
	}
	b.mu.Lock()
	b.artGen++
	gen := b.artGen
	b.mu.Unlock()
	go func() {
		path := ""
		if img != nil {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err == nil {
				path = filepath.Join(artDir(), fmt.Sprintf("tie-audio-%d-cover-%d.png", os.Getpid(), gen))
				if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
					path = ""
				}
			}
		}
		b.mu.Lock()
		if gen != b.artGen {
			b.mu.Unlock()
			if path != "" {
				_ = os.Remove(path)
			}
			return
		}
		old := b.artPath
		b.artPath = path
		b.state.ArtURL = ""
		if path != "" {
			b.state.ArtURL = "file://" + path
		}
		ms := b.state
		b.mu.Unlock()
		if old != "" && old != path {
			_ = os.Remove(old)
		}
		b.server.Update(ms)
	}()
}

// artDir is where cover files go: $XDG_RUNTIME_DIR (per-user, tmpfs) when
// set, else the temp dir.
func artDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

// mprisController routes MPRIS method calls (D-Bus goroutines) to the player;
// the player's actions are goroutine-safe, and window work is marshalled
// onto the UI goroutine.
type mprisController struct{ b *mprisBridge }

func (c mprisController) PlayPause()          { c.b.player.togglePlay() }
func (c mprisController) Play()               { c.b.player.play() }
func (c mprisController) Pause()              { c.b.player.pause() }
func (c mprisController) Stop()               { c.b.player.stop() }
func (c mprisController) Next()               { c.b.player.next() }
func (c mprisController) Previous()           { c.b.player.previous() }
func (c mprisController) SeekTo(sec float64)  { c.b.player.seekTo(sec) }
func (c mprisController) SetVolume(v float64) { c.b.player.setVolume(v) }
func (c mprisController) Raise() {
	fyne.Do(func() {
		c.b.win.Show()
		c.b.win.RequestFocus()
	})
}
func (c mprisController) Quit() {
	fyne.Do(func() { c.b.win.Close() })
}
