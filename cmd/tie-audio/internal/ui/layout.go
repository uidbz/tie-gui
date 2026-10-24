package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
)

// compactForWidth decides the layout from the user's preference and the window
// width. An explicit preference wins outright; "auto" keeps touch devices in
// the compact layout at every orientation. A phone rotated to landscape is
// still a phone: switching it to the split desktop layout makes the panes
// finger-hostile, and the Now Playing page adapts to the wide shape itself
// (cover left, controls right). Desktop windows remain regular unless pinned.
func compactForWidth(pref string, platform *gallery.Platform, width float32) bool {
	switch pref {
	case config.LayoutCompact:
		return true
	case config.LayoutRegular:
		return false
	}
	if platform == nil {
		return false
	}
	if platform.IsMobile() {
		return true
	}
	return platform.CompactLayout(width)
}

// widthWatcher is a transparent widget that reports its own laid-out width.
// Fyne has no window-resize callback, so this is how the shell learns that the
// window crossed the compact-layout threshold (a phone rotating to landscape,
// or a desktop window narrowed). It is stacked *below* the content and draws
// nothing, so it never intercepts pointer events.
//
// The same trick the gallery uses for its pagination link count
// (gallery.sizeWatcher); it lives here too because the shell's content is not
// the gallery's.
type widthWatcher struct {
	widget.BaseWidget
	bg       *canvas.Rectangle
	onWidth  func(width float32)
	lastSeen float32
}

func newWidthWatcher(onWidth func(width float32)) *widthWatcher {
	w := &widthWatcher{bg: canvas.NewRectangle(color.Transparent), onWidth: onWidth}
	w.ExtendBaseWidget(w)
	return w
}

func (w *widthWatcher) Resize(size fyne.Size) {
	w.BaseWidget.Resize(size)
	if size.Width == w.lastSeen {
		return
	}
	w.lastSeen = size.Width
	if w.onWidth != nil {
		w.onWidth(size.Width)
	}
}

func (w *widthWatcher) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.bg)
}
