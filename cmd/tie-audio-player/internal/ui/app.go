// Package ui builds the tie-audio-player Fyne interface: the album browser,
// album/track views, the now-playing queue, and settings.
package ui

import (
	"path"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
)

// shellWindow wraps the real window so every SetContent keeps the transport bar
// pinned at the bottom. The imgview gallery owns the window and calls
// SetContent itself on navigation, so pinning the bar at the window level (via
// this wrapper) is the only way it survives moving between the cover wall,
// album, and settings views.
//
// On desktop the shell also pins the play queue as a resizable right-hand pane
// (an HSplit whose trailing child is queuePanel), so the queue sits beside every
// view. The split instance is kept across navigations so the user's divider
// position survives the gallery's own SetContent calls. On mobile there is no
// pane — the queue is a separate full-screen view reached by swiping.
type shellWindow struct {
	fyne.Window
	bar        fyne.CanvasObject
	queuePanel fyne.CanvasObject
	isMobile   bool
	split      *container.Split
}

// wrap composes o with the transport bar (and, on desktop, the queue pane)
// without pushing it to the window, so Root() can reuse the same layout.
func (w *shellWindow) wrap(o fyne.CanvasObject) fyne.CanvasObject {
	if !w.isMobile && w.queuePanel != nil {
		if w.split == nil {
			w.split = container.NewHSplit(o, w.queuePanel)
			w.split.SetOffset(0.72)
		} else {
			w.split.Leading = o
		}
		w.split.Refresh()
		return container.NewBorder(nil, w.bar, nil, nil, w.split)
	}
	return container.NewBorder(nil, w.bar, nil, nil, o)
}

// SetContent wraps o with the transport bar (and desktop queue pane).
func (w *shellWindow) SetContent(o fyne.CanvasObject) {
	w.Window.SetContent(w.wrap(o))
}

// App is the top-level UI controller. The gallery browser owns the window;
// sub-views (album track list, settings) swap the window content and restore
// the cover wall via the gallery. A shellWindow pins the transport bar to the
// bottom of every view. Changing settings rebuilds the session.
type App struct {
	win       fyne.Window
	session   *data.Session
	browse    *browsePage
	transport *transportBar
	queue     *queuePage
}

// NewApp builds the UI for the given window and session.
func NewApp(win fyne.Window, session *data.Session) *App {
	a := &App{session: session}

	mobile := fyne.CurrentDevice().IsMobile()

	a.transport = newTransportBar(session.Backend)
	// Re-label queue entries whose metadata this app didn't register itself
	// (e.g. a queue that pwplay kept across an app restart): the queue URL's
	// last path segment is the tie content hash.
	a.transport.SetResolver(func(url string) (data.Track, bool) {
		return session.TrackForHash(path.Base(url))
	})
	shell := &shellWindow{Window: win, bar: a.transport.Object(), isMobile: mobile}
	a.win = shell

	// Build the queue before the browse page: constructing the gallery triggers
	// the gallery's own SetContent, and the desktop shell needs queuePanel set by
	// then so the very first render already includes the queue pane. The browse
	// callbacks are reached via closures over a.browse, which is assigned below.
	a.queue = newQueuePage(shell, session, a.transport, mobile,
		func() { a.browse.showBrowse() },
		func(keys []string) { a.browse.saveQueueColumns(keys) },
	)
	if !mobile {
		shell.queuePanel = a.queue.object
	}

	a.browse = newBrowsePage(fyne.CurrentApp(), shell, session)
	a.browse.transport = a.transport
	a.browse.onSettings = func() { a.win.SetContent(a.buildSettings()) }

	if mobile {
		// The queue is a full-screen view; the sidebar button and a right→left
		// swipe both open it, a left→right swipe (or the queue's Back button)
		// returns to the wall.
		a.browse.onQueue = func() {
			a.win.SetContent(a.queue.object)
			a.queue.show()
		}
		a.browse.viewer.OnSwipeLeft = func() {
			a.win.SetContent(a.queue.object)
			a.queue.show()
		}
		a.browse.viewer.OnSwipeRight = func() {
			a.queue.hide()
			a.browse.showBrowse()
		}
	} else {
		// The queue is always visible in the right pane, so it subscribes to the
		// status poll immediately rather than on navigation.
		a.queue.show()
		// A cover dragged onto that pane inserts the album at the drop point.
		a.browse.enableAlbumDragToQueue(a.queue)
	}

	win.SetOnClosed(a.transport.Stop)
	a.transport.Start()
	return a
}

// Root returns the object to place as the window content: the cover wall with
// the transport bar pinned at the bottom (and, on desktop, the queue pane).
func (a *App) Root() fyne.CanvasObject {
	shell, ok := a.win.(*shellWindow)
	if !ok {
		return container.NewBorder(nil, a.transport.Object(), nil, nil, a.browse.Content())
	}
	return shell.wrap(a.browse.Content())
}
