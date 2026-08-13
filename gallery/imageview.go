package gallery

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"os"
	"path/filepath"

	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/widget"

	"github.com/disintegration/imaging"
)

type ImageView struct {
	widget.BaseWidget

	fyneImage         *canvas.Image
	raster            *canvas.Raster
	format            string
	imgWidth          int
	imgHeight         int
	zoom              float32
	imgScale          float32
	region            *Region
	startPos          fyne.Position
	pos               fyne.Position
	size              fyne.Size
	lastContainerSize fyne.Size
	refreshBilinear   *time.Timer
	newSize           bool
	fillWindow        bool
	info              *ImageInfo
	dragStart         bool
	showTagger        bool
	focus             func(fyne.Focusable)
	hotkeys           []Hotkey
	w                 fyne.Window
	container         *fyne.Container
	changeFn          func()
	toggleFullscreen  func()
	platform          *Platform

	// nextFn/prevFn page to the adjacent image; wired by Viewer.ChangeImage so
	// a mobile swipe/flick can navigate the same way the keyboard hotkeys do.
	nextFn func()
	prevFn func()

	// Mobile gesture state (touch only; desktop uses Dragged/Scrolled above).
	// touches holds the live position of each active finger keyed by its
	// TouchEvent.ID; pinchDist/pinchZoom capture the baseline at pinch start;
	// swipeAccum accumulates horizontal drag past the pan border to trigger
	// paging; swipeVertAccum accumulates upward drag past the top border to
	// trigger OnSwipeUp. See TouchDown/TouchMoved/DragEnd.
	touches        map[int]fyne.Position
	pinchDist      float32
	pinchZoom      float32
	swipeAccum     float32
	swipeVertAccum float32
}

type Hotkey struct {
	Name     fyne.KeyName
	Function func()
}

type ImageLayout struct{}

func (d *ImageLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(100, 100)
}
func (d *ImageLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	for _, o := range objects {
		iv := o.(*ImageView)
		if iv.fillWindow {
			iv.pos = fyne.NewPos(0, 0)
			iv.size = containerSize
			iv.newSize = true
		} else {
			if iv.lastContainerSize.Height == 0 {
				iv.lastContainerSize = containerSize
			}
			if iv.lastContainerSize.Height != containerSize.Height || iv.lastContainerSize.Width != containerSize.Width {
				xCenter := (containerSize.Width - iv.lastContainerSize.Width) / 2
				yCenter := (containerSize.Height - iv.lastContainerSize.Height) / 2
				newX := iv.pos.X + xCenter
				newY := iv.pos.Y + yCenter
				iv.lastContainerSize = containerSize
				iv.pos = fyne.NewPos(newX, newY)
			}
		}
		if o.(*ImageView).newSize {
			oldSize := o.Size()
			newSize := iv.size
			o.Resize(newSize)
			iv.newSize = false
			if iv.dragStart { // If dragging while zooming
				delta := newSize.Subtract(oldSize)
				iv.startPos = iv.startPos.Add(fyne.NewPos(delta.Width/2, delta.Height/2))
			}
			iv.changeFn()
		}
		o.Move(iv.pos)
	}
}

func (iv *ImageView) GetImageInfo() string {
	return filepath.Base(iv.info.Path) + " (" + strconv.Itoa(iv.imgWidth) + "x" + strconv.Itoa(iv.imgHeight) + ")" + " [" + strconv.Itoa(iv.GetZoomLevel()) + "%]"
}

func (iv *ImageView) GetZoomLevel() int {
	zoomLevel := iv.Size().Width / float32(iv.imgWidth) * 100
	return int(zoomLevel)
}

func (iv *ImageView) RotateLeft() {
	iv.fyneImage.Image = imaging.Rotate90(iv.fyneImage.Image)
	iv.fyneImage.Refresh()
}

func (iv *ImageView) RotateRight() {
	iv.fyneImage.Image = imaging.Rotate270(iv.fyneImage.Image)
	iv.fyneImage.Refresh()
}

func (iv *ImageView) OriginalSize() {
	iv.fillWindow = false
	iv.size = fyne.NewSize(float32(iv.imgWidth), float32(iv.imgHeight))
	w, h := iv.container.Size().Subtract(iv.size).Components()
	iv.pos = fyne.NewPos(w/2, h/2)
	iv.newSize = true
	iv.container.Refresh()
	iv.changeFn()
}

func NewImageView(info *ImageInfo, size fyne.Size, hideRegion bool, w fyne.Window, focusFunc func(fyne.Focusable), platform *Platform) *ImageView {
	iv := &ImageView{
		focus:           focusFunc,
		zoom:            2,
		info:            info,
		w:               w,
		size:            size,
		newSize:         true,
		refreshBilinear: time.NewTimer(100 * time.Millisecond),
		touches:         make(map[int]fyne.Position),
		platform:        platform,
	}
	if err := iv.LoadImage(); err != nil {
		// A failed decode leaves fyneImage nil and the renderer's Layout
		// would panic on fyneImage.Resize; show the loading placeholder
		// instead so the view stays usable.
		if img, _, perr := Decode(bytes.NewReader(loading)); perr == nil {
			iv.setImage(img)
		}
	}
	iv.refreshBilinear.Stop()
	// go func() {
	// 	for {
	// 		<-iv.refreshBilinear.C
	// 		iv.fyneImage.ScaleMode = canvas.ImageScaleSmooth
	// 		iv.fyneImage.Refresh()
	// 	}
	// }()

	if !hideRegion {
		iv.region = NewRegion(color.Black, iv)
	}
	iv.ExtendBaseWidget(iv)

	return iv
}

func (img *ImageInfo) GetReader() (io.ReadSeeker, error) {
	if img.InputIsReader {
		r, err := img.CustomReader.GetReader()
		if err != nil {
			return nil, err
		}
		_, err = r.Seek(0, io.SeekStart)
		if err != nil {
			return nil, err
		}

		return r, nil
	}
	if img.InputIsArchive {
		if img.archiveFile == nil {
			return nil, errors.New("Could not read zip file")
		}
		imgReader, err := img.archiveFile.Open(img.Path)
		if err != nil {
			return nil, err
		}
		if file, err := io.ReadAll(imgReader); err != nil {
			return nil, err
		} else {
			return bytes.NewReader(file), nil
		}
	} else {
		imgReader, err := os.Open(img.Path)
		if err != nil {
			return nil, err
		}
		return imgReader, nil
	}
}

func (iv *ImageView) LoadImage() error {
	reader, err := iv.info.GetReader()
	if err != nil {
		return err
	}
	img, format, err2 := Decode(reader)
	if err2 != nil {
		return err2
	}
	iv.format = format
	iv.setImage(iv.downscaleForMobile(img))

	return nil
}

// downscaleForMobile shrinks a decoded image on mobile so the GPU texture that
// gets re-uploaded on every pinch frame stays small. Full-resolution phone
// photos (12MP+) are ~48MB of RGBA; re-uploading that each frame makes pinch
// zoom choppy. We cap the longest edge at 2x the screen's longest edge, which
// leaves headroom for zooming in while keeping the texture a few MB. Desktop
// (and any non-decoded case) is left untouched.
func (iv *ImageView) downscaleForMobile(img image.Image) image.Image {
	if !iv.platform.ShouldDownscaleImages() {
		return img
	}
	screen := fyne.Max(iv.size.Width, iv.size.Height)
	if screen <= 0 {
		return img
	}
	maxEdge := int(screen * 2)
	b := img.Bounds()
	longest := b.Dx()
	if b.Dy() > longest {
		longest = b.Dy()
	}
	if longest <= maxEdge {
		return img
	}
	// imaging.Fit scales to fit within maxEdge x maxEdge, preserving aspect ratio.
	return imaging.Fit(img, maxEdge, maxEdge, imaging.Linear)
}

// setImage replaces the displayed image and records its dimensions.
func (iv *ImageView) setImage(img image.Image) {
	iv.fyneImage = canvas.NewImageFromImage(img)
	iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	iv.fyneImage.FillMode = canvas.ImageFillContain
	iv.imgWidth = img.Bounds().Max.X
	iv.imgHeight = img.Bounds().Max.Y
}

func (iv *ImageView) Resize(size fyne.Size) {
	iv.BaseWidget.Resize(size)
}

func (iv *ImageView) Dragged(drag *fyne.DragEvent) {
	if !iv.info.IsDraggable {
		return
	}
	if iv.platform.UsesMobileDragGestures() {
		iv.draggedMobile(drag)
		return
	}
	if !iv.dragStart {
		iv.newSize = false
		iv.startPos = drag.PointEvent.Position
		iv.dragStart = true
		iv.fillWindow = false
	}
	// iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	iv.pos = drag.AbsolutePosition.Subtract(iv.startPos)
	iv.Move(iv.pos)

	// iv.container.Refresh()
}

func (iv *ImageView) DragEnd() {
	if iv.platform.UsesMobileDragGestures() {
		iv.dragEndMobile()
		return
	}
	iv.dragStart = false
	// iv.fyneImage.ScaleMode = canvas.ImageScaleSmooth
	// iv.fyneImage.Refresh()
}

// --- mobile gestures (touch only) -------------------------------------------
//
// On touch we mimic a phone gallery: a one-finger drag pans within the image
// borders; dragging past a border accumulates "overscroll" that pages to the
// next/previous image on release (a fast flick pages too, thanks to Fyne's
// post-release drag momentum). Two fingers pinch-zoom toward their midpoint.
// Desktop keeps its original free-drag + scroll-wheel zoom above.

// containerSize is the space the image is laid out within (the viewport).
func (iv *ImageView) containerSize() fyne.Size {
	if iv.container != nil {
		return iv.container.Size()
	}
	return iv.size
}

// panBounds returns the inclusive min/max top-left position that keeps the
// widget covering the viewport on each axis. When the widget is smaller than
// the viewport on an axis, min==max==the centered position (no panning).
func (iv *ImageView) panBounds() (minX, maxX, minY, maxY float32) {
	c := iv.containerSize()
	s := iv.size
	if s.Width <= c.Width {
		x := (c.Width - s.Width) / 2
		minX, maxX = x, x
	} else {
		minX, maxX = c.Width-s.Width, 0
	}
	if s.Height <= c.Height {
		y := (c.Height - s.Height) / 2
		minY, maxY = y, y
	} else {
		minY, maxY = c.Height-s.Height, 0
	}
	return
}

func (iv *ImageView) draggedMobile(drag *fyne.DragEvent) {
	// Suppress panning/paging while an active two-finger pinch is in progress.
	// Do not check len(touches) — stale map entries after a pinch ends must
	// not block panning on the surviving finger.
	if iv.pinchDist > 0 {
		return
	}
	if !iv.dragStart {
		iv.newSize = false
		iv.dragStart = true
		iv.fillWindow = false
		iv.swipeAccum = 0
		iv.swipeVertAccum = 0
	}

	minX, maxX, minY, maxY := iv.panBounds()

	// Horizontal: pan within bounds, spill the remainder into overscroll.
	newX := iv.pos.X + drag.Dragged.DX
	switch {
	case newX > maxX:
		iv.pos.X = maxX
		iv.swipeAccum += newX - maxX
	case newX < minX:
		iv.pos.X = minX
		iv.swipeAccum += newX - minX
	default:
		iv.pos.X = newX
		iv.swipeAccum = 0 // reversing toward center clears any page charge
	}

	// Vertical: pan within bounds; spill upward remainder into swipeVertAccum
	// so dragEndMobile can detect an intentional swipe-up gesture.
	newY := iv.pos.Y + drag.Dragged.DY
	switch {
	case newY < minY:
		iv.pos.Y = minY
		iv.swipeVertAccum += newY - minY // negative: dragged past top edge
	case newY > maxY:
		iv.pos.Y = maxY
	default:
		iv.pos.Y = newY
		iv.swipeVertAccum = 0 // reversed back toward center: clear the charge
	}

	iv.Move(iv.pos)
}

func (iv *ImageView) dragEndMobile() {
	iv.dragStart = false

	// End any in-progress pinch here. When the primary finger has dragged, the
	// driver's canvas.tapUp returns early (dragging != nil) and never delivers
	// TouchUp to us, so pinchDist/touches would stay stale after a finger lifts
	// and the surviving finger would keep driving TouchMoved's zoom path. DragEnd
	// is the reliable "primary finger released" signal, so reset pinch state and
	// skip the pan/paging logic (a pinch is not a swipe).
	if iv.pinchDist > 0 {
		iv.pinchDist = 0
		iv.touches = make(map[int]fyne.Position)
		iv.swipeAccum = 0
		iv.swipeVertAccum = 0
		// Snap to the default fillWindow view if the pinch settled inside the
		// fit-to-screen detent (mirrors TouchUp, which is not delivered here).
		if zf := iv.zoomFactor(); zf > 0.86 && zf < 1.14 {
			iv.fillWindow = true
		}
		iv.newSize = true
		iv.changeFn()
		iv.container.Refresh()
		return
	}

	c := iv.containerSize()

	// Horizontal paging threshold. When zoomed in, demand a firmer throw.
	threshold := c.Width * 0.2
	if threshold < 40 {
		threshold = 40
	}
	if _, maxX, _, _ := iv.panBounds(); maxX > 0 || iv.pos.X < 0 {
		threshold = c.Width * 0.4
	}

	acc := iv.swipeAccum
	iv.swipeAccum = 0
	vertAcc := iv.swipeVertAccum
	iv.swipeVertAccum = 0

	// Upward swipe: fires OnSwipeUp when the gesture is predominantly vertical
	// (more vertical overscroll than horizontal) and large enough. Checked
	// before horizontal paging so a clean upward swipe does not also page.
	vertThreshold := c.Height * 0.2
	if vertThreshold < 40 {
		vertThreshold = 40
	}
	absAcc := acc
	if absAcc < 0 {
		absAcc = -absAcc
	}
	if vertAcc <= -vertThreshold && -vertAcc > absAcc {
		if iv.info.OnSwipeUp != nil {
			iv.info.OnSwipeUp()
		}
		return
	}

	switch {
	case acc <= -threshold: // dragged left past the edge -> next
		if iv.nextFn != nil {
			iv.nextFn()
		}
	case acc >= threshold: // dragged right past the edge -> previous
		if iv.prevFn != nil {
			iv.prevFn()
		}
	}
}

func (iv *ImageView) TouchDown(e *mobile.TouchEvent) {
	iv.touches[e.ID] = iv.pos.Add(e.Position) // store in container coords
	if len(iv.touches) == 2 {
		// In the fillWindow (fit) state the widget is container-sized with the
		// image letterboxed inside, so zoomFactor() reads >1 for non-matching
		// aspect ratios and the first pinch frame would jump. Convert to the
		// explicit fit representation (widget == visible image) first; it is
		// visually identical, so the pinch starts smoothly at zoom 1.0.
		if iv.fillWindow {
			iv.fillWindow = false
			iv.size = iv.baseSize()
		}
		iv.pinchDist = iv.touchDistance()
		iv.pinchZoom = iv.zoomFactor()
		iv.swipeAccum = 0
	}
}

func (iv *ImageView) TouchUp(e *mobile.TouchEvent) {
	wasPinching := iv.pinchDist > 0
	delete(iv.touches, e.ID)
	if len(iv.touches) < 2 {
		iv.pinchDist = 0
		// Clear all remaining entries. The surviving finger is tracked by
		// Dragged for panning; a stale map entry here would let TouchMoved
		// mistake it for a second active touch and re-run pinch/resize logic.
		iv.touches = make(map[int]fyne.Position)
	}
	// A pinch mutates iv.size/iv.pos via direct Resize/Move (no Refresh, to
	// stay smooth). When it ends, sync the layout state and update the title
	// once so downstream (zoom %, high-quality raster) reflects the final zoom.
	if wasPinching && iv.pinchDist == 0 {
		// If the pinch settled inside the fit-to-screen detent, snap to the
		// default fillWindow view. Using fillWindow (not a fixed size) means the
		// layout re-fits automatically on orientation/viewport changes.
		if zf := iv.zoomFactor(); zf > 0.86 && zf < 1.14 {
			iv.fillWindow = true
		}
		iv.newSize = true
		iv.changeFn()
		iv.container.Refresh()
	}
}

func (iv *ImageView) TouchCancel(e *mobile.TouchEvent) {
	iv.TouchUp(e)
}

func (iv *ImageView) TouchMoved(e *mobile.TouchEvent) {
	if _, ok := iv.touches[e.ID]; !ok {
		return
	}
	iv.touches[e.ID] = iv.pos.Add(e.Position)
	if iv.pinchDist <= 0 || len(iv.touches) < 2 {
		return
	}
	dist := iv.touchDistance()
	if dist <= 0 {
		return
	}

	// Focal-point zoom: keep the image content under the pinch midpoint fixed
	// as we scale (mirrors the scroll-toward-cursor math in Scrolled).
	focal := iv.touchMidpoint()
	oldSize := iv.size
	fx := (focal.X - iv.pos.X) / oldSize.Width
	fy := (focal.Y - iv.pos.Y) / oldSize.Height

	iv.fillWindow = false
	z := clampF(iv.pinchZoom*(dist/iv.pinchDist), 0.1, 40)

	// Detent at fit-to-screen (z == 1.0, the "fill window" / x-hotkey view).
	// Within a band around 1.0 the zoom change is compressed so the image
	// resists moving through the fit point, making it easy to settle back on
	// the default view instead of overshooting. The pull is strongest at the
	// center and tapers to zero at the band edges to stay continuous. Near the
	// center the displayed zoom advances at ~(1-strength) of the finger's
	// motion, which is the "resistance" the user feels.
	const detentBand float32 = 0.28
	const detentStrength float32 = 0.82
	if d := z - 1.0; d > -detentBand && d < detentBand {
		ad := d
		if ad < 0 {
			ad = -ad
		}
		z += (1.0 - z) * detentStrength * (1 - ad/detentBand)
	}

	base := iv.baseSize()
	newSize := fyne.NewSize(base.Width*z, base.Height*z)

	iv.pos = fyne.NewPos(focal.X-fx*newSize.Width, focal.Y-fy*newSize.Height)
	iv.size = newSize
	iv.newSize = false

	// Re-clamp pan to the new bounds (recenters when the image fits).
	minX, maxX, minY, maxY := iv.panBounds()
	iv.pos.X = clampF(iv.pos.X, minX, maxX)
	iv.pos.Y = clampF(iv.pos.Y, minY, maxY)

	// Resize/move the widget directly instead of container.Refresh(): a full
	// Refresh cascades into canvas.Image.Refresh(), which re-rasterizes the
	// source bitmap every frame and makes the pinch choppy. Resize only
	// relayouts and lets the GPU scale the existing texture. The title/refresh
	// is synced once when the pinch ends (TouchUp).
	iv.Resize(newSize)
	iv.Move(iv.pos)
}

// zoomFactor is the current size relative to the fit-to-viewport base size.
func (iv *ImageView) zoomFactor() float32 {
	base := iv.baseSize()
	if base.Width == 0 {
		return 1
	}
	return iv.size.Width / base.Width
}

// baseSize is the widget size at zoom 1: the image scaled to fit the viewport
// (ImageFillContain), preserving aspect ratio.
func (iv *ImageView) baseSize() fyne.Size {
	c := iv.containerSize()
	if iv.imgWidth == 0 || iv.imgHeight == 0 {
		return c
	}
	iw, ih := float32(iv.imgWidth), float32(iv.imgHeight)
	scale := c.Width / iw
	if s := c.Height / ih; s < scale {
		scale = s
	}
	return fyne.NewSize(iw*scale, ih*scale)
}

func (iv *ImageView) firstTwoTouches() []fyne.Position {
	pts := make([]fyne.Position, 0, 2)
	for _, p := range iv.touches {
		pts = append(pts, p)
		if len(pts) == 2 {
			break
		}
	}
	return pts
}

func (iv *ImageView) touchDistance() float32 {
	pts := iv.firstTwoTouches()
	if len(pts) < 2 {
		return 0
	}
	dx := float64(pts[0].X - pts[1].X)
	dy := float64(pts[0].Y - pts[1].Y)
	return float32(math.Hypot(dx, dy))
}

func (iv *ImageView) touchMidpoint() fyne.Position {
	pts := iv.firstTwoTouches()
	if len(pts) < 2 {
		return fyne.Position{}
	}
	return fyne.NewPos((pts[0].X+pts[1].X)/2, (pts[0].Y+pts[1].Y)/2)
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (iv *ImageView) TypedKey(key *fyne.KeyEvent) {
	for _, x := range iv.hotkeys {
		if key.Name == x.Name {
			x.Function()
		}
	}
}
func (iv *ImageView) Tapped(_ *fyne.PointEvent) {
	// On mobile, tapping toggles fullscreen (hides/shows Android system bars)
	// before invoking any custom tap handler, matching Samsung Gallery behavior.
	if iv.platform.IsMobile() && iv.toggleFullscreen != nil {
		iv.toggleFullscreen()
	}
	if iv.info.OnTapped != nil {
		iv.info.OnTapped()
	}
}

func (iv *ImageView) TappedSecondary(_ *fyne.PointEvent) {
	if iv.info.OnTappedSecondary != nil {
		iv.info.OnTappedSecondary()
	}
}

func (iv *ImageView) DoubleTapped(_ *fyne.PointEvent) {
	if iv.info.OnDoubleTapped != nil {
		iv.info.OnDoubleTapped()
	}
}

func (e *ImageView) RegisterKey(name fyne.KeyName, function func()) {
	e.hotkeys = append(e.hotkeys, Hotkey{name, function})
}

func (iv *ImageView) Scrolled(ev *fyne.ScrollEvent) {
	if !iv.info.IsZoomable {
		return
	}
	iv.fillWindow = false
	// iv.fyneImage.ScaleMode = canvas.ImageScaleFastest
	var zoom float32
	if ev.Scrolled.DY > 0 {
		zoom = 1 + (iv.zoom / 10)
	} else {
		zoom = 1 - (iv.zoom / 10)
	}

	newWidth := iv.Size().Width * zoom
	newHeight := iv.Size().Height * zoom
	newSize := fyne.NewSize(newWidth, newHeight)

	factorX := (newWidth / iv.Size().Width)
	factorY := (newHeight / iv.Size().Height)
	x := ev.AbsolutePosition.X - (ev.Position.X * factorX)
	y := ev.AbsolutePosition.Y - (ev.Position.Y * factorY)
	// fmt.Println("delta", ev.Position.X, ev.Position.Y, x, y)
	newPos := fyne.NewPos(x, y)
	iv.pos = newPos
	iv.size = newSize
	iv.newSize = true

	iv.changeFn()
	iv.container.Refresh()
	// iv.refreshBilinear.Reset(100 * time.Millisecond)
}

func (iv *ImageView) FocusGained() {
}

func (iv *ImageView) FocusLost() {
}

func (iv *ImageView) TypedRune(r rune) {
}

/*****************************
** RENDERER
*****************************/
type ImageViewRenderer struct {
	imageView *ImageView
	objects   []fyne.CanvasObject
}

func (r *ImageView) CreateRenderer() fyne.WidgetRenderer {
	ren := &ImageViewRenderer{
		imageView: r,
		objects:   []fyne.CanvasObject{r.fyneImage},
	}

	return ren
}

// Refresh causes this object to be redrawn in it's current state
func (r *ImageViewRenderer) Destroy() {
}

func (ren *ImageViewRenderer) Refresh() {}

func (ren *ImageViewRenderer) Objects() []fyne.CanvasObject {
	if ren.imageView.region == nil {
		return ren.objects
	} else {
		return []fyne.CanvasObject{ren.imageView.fyneImage, ren.imageView.region}
	}
}

func (ren *ImageViewRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, 0)
}

func (ren *ImageViewRenderer) Layout(s fyne.Size) {
	if ren.imageView.fyneImage == nil {
		return
	}
	ren.imageView.fyneImage.Resize(s)
}
