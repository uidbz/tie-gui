package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
)

// enableAlbumDragToQueue wires the gallery's tile-drag hooks so a cover can be
// dragged onto the desktop playlist pane and dropped at a chosen position.
// Called by the App shell on desktop with the playlist page; a no-op setup on
// mobile (where the gallery attaches no drag catcher and there is no pane).
func (b *browsePage) enableAlbumDragToQueue(queue *queuePage) {
	if b.mobile || queue == nil {
		return
	}
	b.queue = queue
	b.queuePanel = queue.object
	b.viewer.OnTileDragStart = b.startAlbumDrag
	b.viewer.OnTileDragged = b.moveAlbumDrag
	b.viewer.OnTileDragEnd = b.endAlbumDrag
}

// albumForTile resolves the album backing a cover tile, or false for a
// non-album tile.
func albumForTile(t *gallery.Tile) (data.Album, bool) {
	if t == nil || t.Info == nil {
		return data.Album{}, false
	}
	if item, ok := t.Info.CustomReader.(*AudioAlbumItem); ok {
		return item.album, true
	}
	return data.Album{}, false
}

// startAlbumDrag records the picked-up album so the drop knows what to enqueue.
// The ghost itself is created on the first move (which carries a position).
func (b *browsePage) startAlbumDrag(t *gallery.Tile) {
	a, ok := albumForTile(t)
	if !ok {
		return
	}
	b.dragAlbum = a
}

// moveAlbumDrag shows/moves a floating label under the cursor naming the album
// being dragged, so the reorder gesture has a visible drag cue. Over the
// playlist pane the label gains a "→ Playlist" hint and an insertion line marks
// where the album will land, matching the intra-playlist reorder feedback.
func (b *browsePage) moveAlbumDrag(t *gallery.Tile, pos fyne.Position) {
	text := b.dragAlbum.Display()
	if b.overQueue(pos) {
		text += "  →  Playlist"
		b.queue.dropLineAt(pos)
	} else {
		b.queue.clearDropLine()
	}
	if b.dragGhost == nil {
		lbl := widget.NewLabel(text)
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		b.dragGhost = widget.NewPopUp(lbl, b.win.Canvas())
	} else if lbl, ok := b.dragGhost.Content.(*widget.Label); ok {
		lbl.SetText(text)
	}
	b.dragGhost.ShowAtPosition(pos.AddXY(12, 8))
}

// endAlbumDrag dismisses the cue and insertion line and, when the drop landed on
// the playlist pane, inserts the album at the dropped position.
func (b *browsePage) endAlbumDrag(t *gallery.Tile, pos fyne.Position) {
	if b.dragGhost != nil {
		b.dragGhost.Hide()
		b.dragGhost = nil
	}
	b.queue.clearDropLine()
	if b.overQueue(pos) {
		b.albumInsertAt(b.dragAlbum, b.queue.gapAt(pos))
	}
	b.dragAlbum = data.Album{}
}

// overQueue reports whether an absolute position falls within the playlist pane.
func (b *browsePage) overQueue(pos fyne.Position) bool {
	if b.queuePanel == nil {
		return false
	}
	origin := fyne.CurrentApp().Driver().AbsolutePositionForObject(b.queuePanel)
	size := b.queuePanel.Size()
	return pos.X >= origin.X && pos.X <= origin.X+size.Width &&
		pos.Y >= origin.Y && pos.Y <= origin.Y+size.Height
}
