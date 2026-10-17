// Keyboard: the gallery's own KeyPress, the default hotkey table, and the
// per-app overrides.
package gallery

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

func (viewer *Gallery) KeyPress(key *fyne.KeyEvent) {
	// While a video plays it fills the main window; route keys to playback
	// controls and skip the grid/image hotkeys (which reference no video state).
	if viewer.currentVideo != nil {
		bindings := viewer.config.Image
		if key.Name == fyne.KeySpace {
			viewer.currentVideo.TogglePlay()
			return
		}
		// Android/iOS hardware back returns to the grid.
		if key.Name == "Back" {
			viewer.showGallery()
			return
		}
		for _, x := range bindings.FullScreen {
			if key.Name == fyne.KeyName(x) {
				viewer.toggleVideoFullscreen()
				return
			}
		}
		for _, x := range bindings.ShowGallery {
			if key.Name == fyne.KeyName(x) {
				viewer.showGallery()
				return
			}
		}
		for _, x := range bindings.Quit {
			if key.Name == fyne.KeyName(x) {
				viewer.showGallery()
				return
			}
		}
		return
	}
	for _, x := range viewer.layout.hotkeys {
		if key.Name == x.Name {
			x.Function()
		}
	}
	// On mobile the image view is not focused (to suppress the soft keyboard),
	// so image-viewer hotkeys (including the Back key) never reach TypedKey.
	// Handle them here instead.
	if viewer.platform.ShouldHandleHotkeysAtWindowLevel() {
		for _, x := range viewer.hotkeys {
			if key.Name == x.Name {
				x.Function()
			}
		}
	}
}

func (viewer *Gallery) InitHotkeys() {
	viewer.hotkeys = []Hotkey{}
	bindings := viewer.config.Image

	add := func(h Hotkey) {
		viewer.hotkeys = append(viewer.hotkeys, h)
	}

	for _, x := range bindings.NextImage {
		add(Hotkey{x, func() {
			viewer.ChangeImage(viewer.NextImage())
		}})
	}
	for _, x := range bindings.PreviousImage {
		add(Hotkey{x, func() {
			viewer.ChangeImage(viewer.PrevImage())
		}})
	}
	for _, x := range bindings.RotateLeft {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.RotateLeft()
		}})
	}
	for _, x := range bindings.RotateRight {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.RotateRight()
		}})
	}
	for _, x := range bindings.OriginalSize {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.OriginalSize()
		}})
	}
	showGallery := viewer.showGallery
	for _, x := range bindings.ShowGallery {
		add(Hotkey{x, showGallery})
	}
	for _, x := range bindings.ToggleInfo {
		add(Hotkey{x, func() {
			viewer.ToggleInfoOverlay()
		}})
	}
	// Gallery navigation hotkeys (ScrollDown/ScrollUp/PathLevelUp) live in
	// TileLayout.InitHotkeys: KeyPress dispatches layout.hotkeys on every
	// platform, while viewer.hotkeys reach the desktop ImageView via focus
	// and the window handler only on mobile.
	// On mobile the Android/iOS back button sends key name "Back" to the
	// focused widget (ImageView). Map it to the same show-gallery action so
	// the user can return to the grid without a keyboard.
	if viewer.platform.ShouldRegisterBackButton() {
		add(Hotkey{"Back", showGallery})
	}
	for _, x := range bindings.Quit {
		add(Hotkey{x, func() {
			viewer.app.Quit()
		}})
	}
	for _, x := range bindings.FillWindow {
		add(Hotkey{x, func() {
			viewer.CurrentImageView.fillWindow = true
			viewer.Content.Refresh()
		}})
	}
	for _, x := range bindings.Filtering {
		add(Hotkey{x, func() {
			if viewer.CurrentImageView.fyneImage.ScaleMode == canvas.ImageScaleFastest {
				viewer.CurrentImageView.fyneImage.ScaleMode = canvas.ImageScalePixels
			} else {
				viewer.CurrentImageView.fyneImage.ScaleMode = canvas.ImageScaleFastest
			}
			viewer.Content.Refresh()
		}})
	}
	for _, x := range bindings.FullScreen {
		add(Hotkey{x, func() {
			viewer.ToggleFullscreen()
		}})
	}
	for _, x := range bindings.RunCmda {
		add(Hotkey{x, func() {
			viewer.RunCmdA()
		}})
	}
	for _, x := range bindings.SaveImage {
		add(Hotkey{x, func() {
			viewer.SaveImage()
		}})
	}
}

// RegisterHotkey binds an additional image-view hotkey. It reaches the
// desktop through the focused ImageView's TypedKey (every ChangeImage hands
// the view the current list) and mobile through the window-level KeyPress,
// exactly like the configured [Image] bindings. Call it after Init, which
// resets the list; bindings are never removed, so register each key once and
// look up any mutable behavior inside the callback.
func (viewer *Gallery) RegisterHotkey(name fyne.KeyName, function func()) {
	viewer.hotkeys = append(viewer.hotkeys, Hotkey{name, function})
}

func (layout *TileLayout) InitHotkeys() {
	layout.hotkeys = []Hotkey{}
	bindings := layout.config.Gallery

	add := func(h Hotkey) {
		layout.hotkeys = append(layout.hotkeys, h)
	}

	for _, x := range bindings.Quit {
		add(Hotkey{x, func() {
			layout.app.Quit()
		}})
	}
	// Gallery navigation hotkeys live in layout.hotkeys because KeyPress
	// dispatches those unconditionally, while Gallery.hotkeys are dispatched
	// at window level only on mobile — on desktop they reach the focused
	// ImageView instead. No widget is focused in the gallery grid, so
	// registering these on Gallery.hotkeys left them dead on desktop.
	scrollBy := func(dy float32) {
		if layout.viewer == nil || layout.viewer.scroll == nil {
			return
		}
		s := layout.viewer.scroll
		s.ScrollToOffset(fyne.NewPos(s.Offset.X, s.Offset.Y+dy))
	}
	for _, x := range bindings.ScrollDown {
		add(Hotkey{x, func() { scrollBy(300) }})
	}
	for _, x := range bindings.ScrollUp {
		add(Hotkey{x, func() { scrollBy(-300) }})
	}
	for _, x := range bindings.PathLevelUp {
		add(Hotkey{x, func() {
			if layout.viewer != nil {
				layout.viewer.ShowImageDir(filepath.Dir(layout.viewer.currentPath))
			}
		}})
	}
	// Skip ToggleFilenames on mobile (no keyboard to press N)
	if !layout.viewer.Platform().IsMobile() {
		for _, x := range bindings.ToggleFilenames {
			add(Hotkey{x, func() {
				layout.ToggleLabels()
			}})
		}
	}
}
