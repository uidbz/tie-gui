// The Tile widget: the placeholder and finished tile, its renderer, the tap and
// swipe gestures, and the two overlay widgets that route them.
package gallery

import (
	"image"
	"image/color"
	"io"

	_ "embed"

	"fyne.io/fyne/v2"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/tielog"
)

// labelHeight is the fixed pixel height reserved below each tile for the name
// label when filenames are shown.
const labelHeight = float32(22)

type Tile struct {
	widget.BaseWidget
	Content   *canvas.Image
	width     float32
	height    float32
	landscape bool
	Viewer    *Gallery
	Info      *ImageInfo
	tabFn     func(t *Tile)
	nameLabel *widget.Label
	subLabel  *widget.Label // optional second line (e.g. artist), nil when unused
	layout    *TileLayout   // reference for showLabels state
	// swipeOverlay covers directory/archive tiles (PreviewPaths != nil) and
	// turns horizontal drags into preview cycling; nil for regular images.
	swipeOverlay *dirSwipeOverlay
	// dragOverlay covers the whole tile when the viewer enables tile dragging
	// (OnTileDragged set, desktop); it forwards taps and drives the drag hooks.
	dragOverlay *tileDragOverlay
}

func (layout *TileLayout) NewImageTile(reader io.ReadSeeker, info *ImageInfo, tabFn func(t *Tile)) (*Tile, error) {
	bitmap, err := decodeThumbnailRGBA(reader)
	if err != nil {
		return nil, err
	}
	return layout.newImageTileFromImage(bitmap, info, tabFn), nil
}

// newImageTileFromImage builds a tile around an already-decoded image.
// PlaceTiles uses it to share one decoded placeholder across all tiles on a
// page instead of decoding loading.png once per tile. The image is not
// mutated afterwards, so sharing it between canvas.Image objects is safe.
func (layout *TileLayout) newImageTileFromImage(decoded image.Image, info *ImageInfo, tabFn func(t *Tile)) *Tile {
	t := &Tile{
		Viewer: layout.viewer,
		layout: layout,
	}
	img := canvas.NewImageFromImage(decoded) // do not resize if picture is smaller than tile

	img.ScaleMode = canvas.ImageScaleFastest
	img.FillMode = canvas.ImageFillContain
	t.Info = info
	if w, h := info.Dimensions(); w > 0 && h > 0 {
		// Use pre-stored original dimensions so placeholder tiles already
		// carry the correct aspect ratio, avoiding layout reflow on load.
		t.width = float32(w)
		t.height = float32(h)
	} else {
		t.width = float32(img.Image.Bounds().Max.X)
		t.height = float32(img.Image.Bounds().Max.Y)
	}
	t.landscape = t.width > t.height
	t.Content = img
	t.tabFn = tabFn

	// Create name label using the entry's display name. When the entry has a
	// subtitle, the name renders bold with the subtitle below it in normal
	// weight (e.g. album title over artist).
	if info.DisplayName != "" {
		lbl := widget.NewLabel(info.DisplayName)
		lbl.Alignment = fyne.TextAlignCenter
		lbl.Truncation = fyne.TextTruncateEllipsis
		if info.Subtitle != "" {
			lbl.TextStyle = fyne.TextStyle{Bold: true}
		}
		if !layout.showLabels {
			lbl.Hide()
		}
		t.nameLabel = lbl
	}
	if info.Subtitle != "" {
		sub := widget.NewLabel(info.Subtitle)
		sub.Alignment = fyne.TextAlignCenter
		sub.Truncation = fyne.TextTruncateEllipsis
		if !layout.showLabels {
			sub.Hide()
		}
		t.subLabel = sub
	}

	t.ExtendBaseWidget(t)

	return t
}

func (t *Tile) Tapped(_ *fyne.PointEvent) {
	t.tabFn(t)
}

func (t *Tile) TappedSecondary(_ *fyne.PointEvent) {
	if t.Viewer != nil && t.Viewer.OnTileSecondaryTapped != nil {
		t.Viewer.OnTileSecondaryTapped(t)
	}
}

// cyclePreview advances the preview shown on a directory/archive/video tile
// by delta (+1 = next, -1 = previous, wrapping around): the image within a
// folder/archive, or the frame within a video. The new thumbnail is
// generated off-thread and swapped into the existing tile in place, so only
// this tile's texture is re-uploaded.
func (t *Tile) cyclePreview(delta int) {
	info := t.Info
	n := info.PreviewCount()
	if n == 0 || t.layout == nil {
		return
	}
	info.AdvancePreview(delta, n)
	go func() {
		defer tielog.Recover(t.layout.logger(), "gallery.cyclePreview")
		thumb, err := t.layout.GetThumbnail(info)
		if err != nil {
			t.layout.logger().Debug("gallery: preview thumbnail", "path", info.Path, "err", err)
			return
		}
		decoded, _, err := Decode(thumb)
		if err != nil || decoded == nil {
			return
		}
		rgba := toRGBA(decoded)
		t.layout.postUI(func() {
			if t.Info != info {
				return // tile was replaced while loading
			}
			t.Content.Image = rgba
			b := rgba.Bounds()
			t.width, t.height = float32(b.Dx()), float32(b.Dy())
			t.landscape = t.width > t.height
			t.Content.Refresh() // invalidate only this tile's texture
			t.layout.relayoutGrid()
		})
	}()
}

// swipeThreshold is the horizontal drag distance in pixels that triggers a
// preview change on directory/archive tiles.
const swipeThreshold = 40

// dirSwipeOverlay is a transparent widget covering the image area of tiles
// with cyclable previews (directories, archives, videos). It forwards taps to
// the tile (so the entry still opens) and turns horizontal drags into preview
// cycling (next/previous image inside the folder/archive, or next/previous
// frame of a video). It only exists on tiles whose ImageInfo.HasPreviews is
// true, so regular image tiles never intercept drags and the scroll container
// keeps its native drag/fling behavior there. Vertical drags are forwarded to
// the gallery's scroller so page scrolling still works when the gesture
// starts on an overlaid tile (only fling momentum is lost in that case).
type dirSwipeOverlay struct {
	widget.BaseWidget
	tile   *Tile
	bg     *canvas.Rectangle
	accumX float32
	accumY float32
}

func newDirSwipeOverlay(t *Tile) *dirSwipeOverlay {
	o := &dirSwipeOverlay{tile: t, bg: canvas.NewRectangle(color.Transparent)}
	o.ExtendBaseWidget(o)
	return o
}

func (o *dirSwipeOverlay) Tapped(ev *fyne.PointEvent)          { o.tile.Tapped(ev) }
func (o *dirSwipeOverlay) TappedSecondary(ev *fyne.PointEvent) { o.tile.TappedSecondary(ev) }

func (o *dirSwipeOverlay) Dragged(ev *fyne.DragEvent) {
	o.accumX += ev.Dragged.DX
	o.accumY += ev.Dragged.DY
	absX, absY := o.accumX, o.accumY
	if absX < 0 {
		absX = -absX
	}
	if absY < 0 {
		absY = -absY
	}
	if absX > absY && absX >= swipeThreshold {
		// Swipe left (finger moves left) = next image, right = previous.
		delta := 1
		if o.accumX > 0 {
			delta = -1
		}
		o.accumX, o.accumY = 0, 0
		o.tile.cyclePreview(delta)
		return
	}
	// Vertical-dominant (or still sub-threshold) drag: keep the page
	// scrolling by forwarding the delta to the gallery's scroller.
	if v := o.tile.Viewer; v != nil && v.scroll != nil && ev.Dragged.DY != 0 {
		v.scroll.ScrollToOffset(fyne.NewPos(v.scroll.Offset.X, v.scroll.Offset.Y-ev.Dragged.DY))
	}
}

func (o *dirSwipeOverlay) DragEnd() { o.accumX, o.accumY = 0, 0 }

func (o *dirSwipeOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(o.bg)
}

// tileDragThreshold is the pointer travel (px) before a press on a tile is
// treated as a drag rather than a click, so small jitters still open the entry.
const tileDragThreshold = 6

// tileDragOverlay is a transparent full-tile catcher attached when the viewer
// enables tile dragging (OnTileDragged set, desktop). It forwards taps and the
// secondary tap so the entry still opens/menus, and turns a press-and-drag into
// the viewer's OnTileDrag* hooks with the pointer's absolute position for
// cross-widget drop hit-testing. Regular image tiles carry no other overlay, so
// this one owns their gestures; it is never attached on preview tiles (which use
// dirSwipeOverlay) or on mobile.
type tileDragOverlay struct {
	widget.BaseWidget
	tile    *Tile
	bg      *canvas.Rectangle
	started bool
	accum   fyne.Position
	last    fyne.Position
}

func newTileDragOverlay(t *Tile) *tileDragOverlay {
	o := &tileDragOverlay{tile: t, bg: canvas.NewRectangle(color.Transparent)}
	o.ExtendBaseWidget(o)
	return o
}

func (o *tileDragOverlay) Tapped(ev *fyne.PointEvent)          { o.tile.Tapped(ev) }
func (o *tileDragOverlay) TappedSecondary(ev *fyne.PointEvent) { o.tile.TappedSecondary(ev) }

func (o *tileDragOverlay) Dragged(ev *fyne.DragEvent) {
	o.last = ev.AbsolutePosition
	v := o.tile.Viewer
	if v == nil || v.OnTileDragged == nil {
		return
	}
	if !o.started {
		o.accum.X += ev.Dragged.DX
		o.accum.Y += ev.Dragged.DY
		if abs32(o.accum.X) < tileDragThreshold && abs32(o.accum.Y) < tileDragThreshold {
			return // still within the click tolerance
		}
		o.started = true
		if v.OnTileDragStart != nil {
			v.OnTileDragStart(o.tile)
		}
	}
	v.OnTileDragged(o.tile, ev.AbsolutePosition)
}

func (o *tileDragOverlay) DragEnd() {
	started := o.started
	o.started = false
	o.accum = fyne.Position{}
	if !started {
		return
	}
	if v := o.tile.Viewer; v != nil && v.OnTileDragEnd != nil {
		v.OnTileDragEnd(o.tile, o.last)
	}
}

func (o *tileDragOverlay) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(o.bg)
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func (ta *Tile) CreateRenderer() fyne.WidgetRenderer {
	r := &TileRenderer{tile: ta}
	r.objects = []fyne.CanvasObject{ta.Content}
	// Tiles with cyclable previews (directories, archives, videos) get a
	// transparent swipe overlay above the image. Later objects win
	// hit-testing, so the overlay (not the tile) receives taps/drags and
	// forwards them appropriately.
	if ta.swipeOverlay == nil && ta.Info != nil && ta.Info.HasPreviews() {
		ta.swipeOverlay = newDirSwipeOverlay(ta)
	}
	if ta.swipeOverlay != nil {
		r.objects = append(r.objects, ta.swipeOverlay)
	}
	// A plain image tile (no preview swipe overlay) carries a full-tile drag
	// catcher when the viewer opts into tile dragging on desktop.
	if ta.dragOverlay == nil && ta.swipeOverlay == nil && ta.Viewer != nil &&
		ta.Viewer.OnTileDragged != nil && !ta.Viewer.Platform().IsMobile() {
		ta.dragOverlay = newTileDragOverlay(ta)
	}
	if ta.dragOverlay != nil {
		r.objects = append(r.objects, ta.dragOverlay)
	}
	if ta.nameLabel != nil {
		r.objects = append(r.objects, ta.nameLabel)
	}
	if ta.subLabel != nil {
		r.objects = append(r.objects, ta.subLabel)
	}
	return r
}

// TileRenderer renders the tile image with an optional name label below it.
type TileRenderer struct {
	tile *Tile
	// objects is precomputed once at renderer creation: the painter calls
	// Objects() for every visible widget on every repaint and on every
	// mouse hit-test walk, so allocating a fresh slice per call cost ~500
	// allocations per repaint on a full page. Hidden labels are skipped by
	// the painter's visibility check, so the slice contents never change.
	objects []fyne.CanvasObject
}

func (r *TileRenderer) Layout(size fyne.Size) {
	imgH := size.Height
	if r.tile.nameLabel != nil && r.tile.nameLabel.Visible() {
		lines := float32(1)
		if r.tile.subLabel != nil {
			lines = 2
		}
		imgH = size.Height - lines*labelHeight
		r.tile.nameLabel.Resize(fyne.NewSize(size.Width, labelHeight))
		r.tile.nameLabel.Move(fyne.NewPos(0, imgH))
		if r.tile.subLabel != nil {
			r.tile.subLabel.Resize(fyne.NewSize(size.Width, labelHeight))
			r.tile.subLabel.Move(fyne.NewPos(0, imgH+labelHeight))
		}
	}
	r.tile.Content.Resize(fyne.NewSize(size.Width, imgH))
	r.tile.Content.Move(fyne.NewPos(0, 0))
	if r.tile.swipeOverlay != nil {
		r.tile.swipeOverlay.Resize(fyne.NewSize(size.Width, imgH))
		r.tile.swipeOverlay.Move(fyne.NewPos(0, 0))
	}
	if r.tile.dragOverlay != nil {
		r.tile.dragOverlay.Resize(fyne.NewSize(size.Width, imgH))
		r.tile.dragOverlay.Move(fyne.NewPos(0, 0))
	}
}

func (r *TileRenderer) MinSize() fyne.Size {
	return fyne.NewSize(50, 50)
}

func (r *TileRenderer) Refresh() {
	r.tile.Content.Refresh()
	if r.tile.nameLabel != nil {
		r.tile.nameLabel.Refresh()
	}
	if r.tile.subLabel != nil {
		r.tile.subLabel.Refresh()
	}
}

func (r *TileRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *TileRenderer) Destroy() {}
