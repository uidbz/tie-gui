package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// The primary style is a filled disc in the theme primary color; the flat
// style has no disc until hovered or pressed.
func TestTransportButtonDiscColors(t *testing.T) {
	test.NewApp()

	primary := newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, nil)
	pr := primary.CreateRenderer().(*transportButtonRenderer)
	wantDisc := theme.Color(theme.ColorNamePrimary)
	if pr.disc.FillColor != wantDisc {
		t.Errorf("primary disc = %v, want %v", pr.disc.FillColor, wantDisc)
	}

	flat := newTransportButton(theme.MediaStopIcon(), transportBarButton, false, nil)
	fr := flat.CreateRenderer().(*transportButtonRenderer)
	if fr.disc.FillColor != color.Transparent {
		t.Errorf("flat disc = %v, want transparent", fr.disc.FillColor)
	}
}

// The icon is tinted to contrast with the disc on the primary style, and
// follows the foreground color on the flat style.
func TestTransportButtonIconTint(t *testing.T) {
	test.NewApp()

	primary := newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, nil)
	pr := primary.CreateRenderer().(*transportButtonRenderer)
	got, ok := pr.icon.Resource.(fyne.ThemedResource)
	if !ok || got.ThemeColorName() != theme.ColorNameForegroundOnPrimary {
		t.Errorf("primary icon tint = %T, want foregroundOnPrimary", pr.icon.Resource)
	}

	flat := newTransportButton(theme.MediaPlayIcon(), transportBarButton, false, nil)
	fr := flat.CreateRenderer().(*transportButtonRenderer)
	got, ok = fr.icon.Resource.(fyne.ThemedResource)
	if !ok || got.ThemeColorName() != theme.ColorNameForeground {
		t.Errorf("flat icon tint = %T, want foreground", fr.icon.Resource)
	}
}

// Hover shows the feedback disc on the desktop and clears when the pointer
// leaves. Each check renders a fresh renderer (CreateRenderer ends in a
// Refresh), so what is asserted is the color the widget state produces.
func TestTransportButtonHover(t *testing.T) {
	test.NewApp()

	b := newTransportButton(theme.MediaSkipNextIcon(), transportBarButton, false, nil)

	b.MouseIn(&desktop.MouseEvent{})
	r := b.CreateRenderer().(*transportButtonRenderer)
	if r.overlay.FillColor != theme.Color(theme.ColorNameHover) {
		t.Errorf("hovered overlay = %v, want hover color", r.overlay.FillColor)
	}
	b.MouseOut()
	r = b.CreateRenderer().(*transportButtonRenderer)
	if r.overlay.FillColor != color.Transparent {
		t.Errorf("unhovered overlay = %v, want transparent", r.overlay.FillColor)
	}
}

// A tap fires the action.
func TestTransportButtonTap(t *testing.T) {
	test.NewApp()

	tapped := 0
	b := newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, func() { tapped++ })
	b.Tapped(nil)
	if tapped != 1 {
		t.Fatalf("onTap fired %d times, want 1", tapped)
	}
}

// SetIcon swaps the rendered icon (the play/pause flip).
func TestTransportButtonSetIcon(t *testing.T) {
	test.NewApp()

	b := newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, nil)
	b.SetIcon(theme.MediaPauseIcon())
	if b.source != theme.MediaPauseIcon() {
		t.Errorf("source = %v, want the pause icon", b.source)
	}
}

// MinSize is the configured square.
func TestTransportButtonMinSize(t *testing.T) {
	test.NewApp()

	b := newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, nil)
	if s := b.MinSize(); s.Width != transportBarPlay || s.Height != transportBarPlay {
		t.Errorf("MinSize = %v, want %v square", s, transportBarPlay)
	}
}
