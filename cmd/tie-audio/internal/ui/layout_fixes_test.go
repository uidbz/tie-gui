package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// The Now Playing page's sliders must span the controls column, not shrink to
// the transport buttons' MinSize (the NewCenter wrapper that caused that).
func TestNowPlayingSlidersSpanFullWidth(t *testing.T) {
	newTestApp(t)
	p := newPlayer(&fakeBackend{}, nil)
	page := newNowPlayingPage(p, func() {})
	w := test.NewWindow(page.Object())
	defer w.Close()
	w.Resize(fyne.NewSize(420, 800))
	page.Object().Resize(fyne.NewSize(420, 800))

	if got := page.seek.Size().Width; got < 320 {
		t.Errorf("seek slider width = %v, want the full controls width", got)
	}
	if got := page.volume.Size().Width; got < 320 {
		t.Errorf("volume slider width = %v, want the full controls width", got)
	}
}

// The mini bar's progress hairline belongs directly under the playlist, above
// the cover/transport row, not at the bottom of the bar.
func TestMiniBarProgressAboveControls(t *testing.T) {
	newTestApp(t)
	p := newPlayer(&fakeBackend{}, nil)
	bar := newMiniBar(p, func() {}, func() {})
	w := test.NewWindow(bar.Object())
	defer w.Close()
	w.Resize(fyne.NewSize(400, 300))
	bar.Object().Resize(fyne.NewSize(400, 300))

	if got := bar.progress.Position().Y; got != 0 {
		t.Errorf("progress Y = %v, want 0 (top of the mini bar)", got)
	}
	if got := bar.progress.Size().Height; got != thinProgressHeight {
		t.Errorf("progress height = %v, want %v", got, thinProgressHeight)
	}
}

// Play album / add to playlist must open the playlist in the same UI turn,
// before the backend's asynchronous queue replacement lands.
func TestPlayAndEnqueueJumpToPlaylist(t *testing.T) {
	newTestApp(t)
	backend := &fakeBackend{}
	p := newPlayer(backend, nil)
	cfg := client.DefaultConfig()
	cfg.FileHosts = map[string]client.FileHost{
		"default": {URL: "http://host:1162"},
	}
	cfg.DefaultFileHosts = []string{"default"}
	sess := &data.Session{Tie: client.NewTieClient(cfg), Backend: backend}
	b := &browsePage{transport: p}
	b.session.Store(sess)

	jumps := 0
	b.showPlaylist = func() { jumps++ }
	tracks := []data.Track{{Hash: "hash", Title: "Track"}}

	b.playAlbumAndShow(tracks)
	if jumps != 1 {
		t.Fatalf("play album jumps = %d, want 1", jumps)
	}
	b.enqueueAndShow(tracks)
	if jumps != 2 {
		t.Fatalf("add to playlist jumps = %d, want 2", jumps)
	}
}
