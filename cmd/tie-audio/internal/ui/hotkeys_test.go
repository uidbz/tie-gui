package ui

import (
	"testing"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// A configured hotkey fires its action; an unbound key does nothing.
func TestKeyPressDispatchesHotkeys(t *testing.T) {
	called := 0
	a := &App{hotkeys: map[fyne.KeyName]func(){
		"Space": func() { called++ },
	}}
	a.keyPress(&fyne.KeyEvent{Name: "Space"})
	if called != 1 {
		t.Fatalf("hotkey fired %d times, want 1", called)
	}
	a.keyPress(&fyne.KeyEvent{Name: "Q"}) // unbound: ignored
	if called != 1 {
		t.Fatalf("unbound key fired the action %d times, want still 1", called)
	}
}

// initHotkeys resolves the config file's table over the defaults and binds
// them on desktop.
func TestInitHotkeysResolvesConfig(t *testing.T) {
	a := &App{
		platform: gallery.NewPlatformFor(false),
		session: &data.Session{Cfg: config.AppConfig{
			Hotkeys: map[string][]string{
				"PlayPause": {"F5"},
				"Stop":      {}, // explicitly unbound
			},
		}},
		player: newPlayer(&fakeBackend{}, nil),
	}
	a.initHotkeys()

	if a.hotkeys["F5"] == nil {
		t.Error("F5 not bound to PlayPause")
	}
	if a.hotkeys["Space"] != nil {
		t.Error("Space still bound although PlayPause was overridden to F5")
	}
	if a.hotkeys["X"] != nil {
		t.Error("X still bound although Stop was explicitly unbound")
	}
	if a.hotkeys["N"] == nil {
		t.Error("N not bound to the default Next")
	}
}

// Mobile builds no hotkeys: there is no keyboard, and the window-level
// handler must stay free for the system back gesture.
func TestInitHotkeysSkippedOnMobile(t *testing.T) {
	a := &App{
		platform: gallery.NewPlatformFor(true),
		session:  &data.Session{Cfg: config.AppConfig{}},
		player:   newPlayer(&fakeBackend{}, nil),
	}
	a.initHotkeys()
	if len(a.hotkeys) != 0 {
		t.Errorf("hotkeys built on mobile: %v", a.hotkeys)
	}
}
