package ui

import (
	"fyne.io/fyne/v2"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
)

// initHotkeys resolves the config file's [Hotkeys] table (merged over the
// defaults by config.ResolveHotkeys) into a key-name → action map, installed
// on the window by syncBackHandler. Desktop only: mobile has no keyboard, and
// leaving the map empty there keeps the window-level handler off so the
// system back gesture still leaves the app on the cover wall.
//
// The actions drive the shared player, so they work from every view. Keys
// pressed while a text entry has keyboard focus never reach the window
// handler (the entry consumes them), so bindings like Space are safe.
func (a *App) initHotkeys() {
	if a.platform.IsMobile() {
		return
	}
	p := a.player
	actions := map[string]func(){
		config.HotkeyPlayPause:    p.togglePlay,
		config.HotkeyStop:         func() { p.do(p.backend.Stop) },
		config.HotkeyNext:         func() { p.do(p.backend.Next) },
		config.HotkeyPrevious:     func() { p.do(p.backend.Previous) },
		config.HotkeySeekForward:  func() { p.seekBy(10) },
		config.HotkeySeekBackward: func() { p.seekBy(-10) },
		config.HotkeyVolumeUp:     func() { p.volumeStep(0.1) },
		config.HotkeyVolumeDown:   func() { p.volumeStep(-0.1) },
	}
	bindings := config.ResolveHotkeys(a.session.Cfg.Hotkeys)
	hotkeys := map[fyne.KeyName]func(){}
	for action, keys := range bindings {
		fn, ok := actions[action]
		if !ok {
			continue // unknown action name in the config file
		}
		for _, k := range keys {
			hotkeys[fyne.KeyName(k)] = fn
		}
	}
	if len(hotkeys) > 0 {
		a.hotkeys = hotkeys
	}
}
