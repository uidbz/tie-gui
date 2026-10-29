package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// In landscape the Now Playing page must use the extra width: cover on the
// left, controls beside it rather than stacked below.
func TestNowPlayingLayoutLandscape(t *testing.T) {
	test.NewApp()
	cover := canvas.NewRectangle(color.Black)
	controls := container.NewVBox(widget.NewLabel("controls"))

	nowPlayingLayout{}.Layout([]fyne.CanvasObject{cover, controls}, fyne.NewSize(800, 400))

	if cover.Position().X != 0 || cover.Position().Y != 0 {
		t.Errorf("landscape cover at %v, want top-left", cover.Position())
	}
	if cover.Size().Width != 400 || cover.Size().Height != 400 {
		t.Errorf("landscape cover size = %v, want 400x400", cover.Size())
	}
	if controls.Position().X != 400 || controls.Position().Y != 0 {
		t.Errorf("landscape controls at %v, want right half", controls.Position())
	}
	if controls.Size().Width != 400 || controls.Size().Height != 400 {
		t.Errorf("landscape controls size = %v, want 400x400", controls.Size())
	}
}

// In portrait the cover stays above the controls, which are pinned to the
// bottom.
func TestNowPlayingLayoutPortrait(t *testing.T) {
	test.NewApp()
	cover := canvas.NewRectangle(color.Black)
	controls := container.NewVBox(widget.NewLabel("controls"))

	nowPlayingLayout{}.Layout([]fyne.CanvasObject{cover, controls}, fyne.NewSize(400, 800))

	if cover.Position() != fyne.NewPos(0, 0) {
		t.Errorf("portrait cover at %v, want top-left", cover.Position())
	}
	if controls.Position().X != 0 || controls.Position().Y < cover.Size().Height-1 {
		t.Errorf("portrait controls at %v, want below the cover", controls.Position())
	}
	if controls.Position().Y+controls.Size().Height != 800 {
		t.Errorf("portrait controls bottom = %v, want 800", controls.Position().Y+controls.Size().Height)
	}
}

func TestDesktopNowPlayingApply(t *testing.T) {
	test.NewApp()
	v := newDesktopNowPlaying()
	v.apply(transportState{title: "Song", subtitle: "Artist · Album"})

	if v.title.Text != "Song" || v.subtitle.Text != "Artist · Album" {
		t.Errorf("labels = %q / %q", v.title.Text, v.subtitle.Text)
	}
}

func TestNowPlayingModeDesktopOnly(t *testing.T) {
	page := &browsePage{}
	page.session.Store(&data.Session{Cfg: config.AppConfig{BrowseView: config.BrowseNowPlaying}})
	if !page.nowPlayingMode() {
		t.Error("desktop page did not report now-playing mode")
	}
	page.compact = true
	if page.nowPlayingMode() {
		t.Error("compact page must use the album views, not the desktop now-playing view")
	}
}

func TestBrowseViewTabsActive(t *testing.T) {
	test.NewApp()
	tabs := newBrowseViewTabs(func(string) {})
	tabs.setActive(config.BrowseNowPlaying)

	if got := tabs.buttons[config.BrowseNowPlaying].Importance; got != widget.HighImportance {
		t.Errorf("Now Playing tab importance = %v, want high", got)
	}
	if got := tabs.buttons[config.BrowseCovers].Importance; got != widget.LowImportance {
		t.Errorf("Covers tab importance = %v, want low", got)
	}
}
