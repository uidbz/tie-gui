package ui

import (
	"image"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Cover sizes, in Fyne device-independent pixels.
const (
	regularCoverSize = 44 // thumb in the desktop transport bar
	miniCoverSize    = 48 // thumb in the compact mini bar
	queueCoverSize   = 32 // cell in the desktop queue's cover column
	groupCoverSize   = 56 // album header in the compact grouped playlist
)

// placeholderCoverColor is the neutral square shown for an album with no
// artwork; it matches the cover wall's placeholder tiles.
var placeholderCoverColor = color.NRGBA{R: 48, G: 48, B: 56, A: 255}

// coverView displays album art with a neutral placeholder behind it, so a
// track whose album has no cover (or whose cover has not loaded yet) still
// occupies the same space instead of making the row jump.
type coverView struct {
	img    *canvas.Image
	object fyne.CanvasObject
}

// newCoverView builds a cover display. A positive size fixes it to a square of
// that edge; size <= 0 lets it expand to whatever the parent gives it (the Now
// Playing page).
func newCoverView(size float32) *coverView {
	c := &coverView{img: &canvas.Image{FillMode: canvas.ImageFillContain, ScaleMode: canvas.ImageScaleFastest}}
	c.img.Hide()
	stack := container.NewStack(canvas.NewRectangle(placeholderCoverColor), c.img)
	if size > 0 {
		c.object = container.NewGridWrap(fyne.NewSize(size, size), stack)
	} else {
		c.object = stack
	}
	return c
}

// set shows img, or the placeholder when img is nil.
func (c *coverView) set(img image.Image) {
	if img == nil {
		c.img.Image = nil
		c.img.Hide()
		return
	}
	c.img.Image = img
	c.img.Show()
	c.img.Refresh()
}

// regularBar is the wide transport bar used by the regular (desktop / tablet)
// layout: transport buttons, the now-playing track with its cover, a seek bar
// and a compact volume slider, all on one row.
type regularBar struct {
	object *fyne.Container
	cover  *coverView
	play   *transportButton
	seek   *widget.Slider
	volume *widget.Slider
	pos    *widget.Label
	dur    *widget.Label
	now    *widget.Label
}

func newRegularBar(p *player) *regularBar {
	b := &regularBar{
		cover:  newCoverView(regularCoverSize),
		seek:   p.newSeekSlider(),
		volume: p.newVolumeSlider(),
		pos:    widget.NewLabel("0:00"),
		dur:    widget.NewLabel("0:00"),
		now:    widget.NewLabel("Nothing playing"),
	}
	b.now.Truncation = fyne.TextTruncateEllipsis

	prev := newTransportButton(theme.MediaSkipPreviousIcon(), transportBarButton, false, func() { p.do(p.backend.Previous) })
	b.play = newTransportButton(theme.MediaPlayIcon(), transportBarPlay, true, p.togglePlay)
	next := newTransportButton(theme.MediaSkipNextIcon(), transportBarButton, false, func() { p.do(p.backend.Next) })
	stop := newTransportButton(theme.MediaStopIcon(), transportBarButton, false, func() { p.do(p.backend.Stop) })

	buttons := container.NewHBox(prev, b.play, next, stop)
	volBox := container.NewCenter(container.NewHBox(
		widget.NewIcon(theme.VolumeUpIcon()),
		container.NewGridWrap(fyne.NewSize(140, 28), b.volume),
	))
	progress := container.NewBorder(nil, nil, b.pos, b.dur, b.seek)
	center := container.NewBorder(nil, nil, b.cover.object, nil,
		container.NewVBox(b.now, progress))

	b.object = container.NewBorder(widget.NewSeparator(), nil, buttons, volBox, center)
	return b
}

func (b *regularBar) Object() fyne.CanvasObject { return b.object }

func (b *regularBar) apply(st transportState) {
	applyPlayIcon(b.play, st.playing)
	label := st.title
	if st.subtitle != "" {
		label += " — " + st.subtitle
	}
	b.now.SetText(label)
	applySeek(b.seek, b.pos, b.dur, st)
	if st.applyVolume {
		b.volume.SetValue(st.volume)
	}
}

func (b *regularBar) setCover(img image.Image) { b.cover.set(img) }

// miniBar is the compact layout's pinned transport: a cover thumb, the track
// title and artist, and just play/pause and next. Everything else — seek,
// volume, prev, stop — lives on the Now Playing page, which the bar opens when
// tapped or swiped up. A phone has no room for a bar wide enough to hold a
// usable seek slider *and* the metadata, and a cramped slider is worse than no
// slider: the whole point of the Now Playing page is that both sliders get the
// full screen width.
type miniBar struct {
	object   *fyne.Container
	cover    *coverView
	play     *transportButton
	title    *widget.Label
	subtitle *widget.Label
	progress *thinProgress
}

func newMiniBar(p *player, onOpen func()) *miniBar {
	b := &miniBar{
		cover:    newCoverView(miniCoverSize),
		title:    widget.NewLabel("Nothing playing"),
		subtitle: widget.NewLabel(""),
		progress: newThinProgress(),
	}
	b.title.Truncation = fyne.TextTruncateEllipsis
	b.title.TextStyle = fyne.TextStyle{Bold: true}
	b.subtitle.Truncation = fyne.TextTruncateEllipsis

	b.play = newTransportButton(theme.MediaPlayIcon(), transportMiniPlay, true, p.togglePlay)
	next := newTransportButton(theme.MediaSkipNextIcon(), transportMiniButton, false, func() { p.do(p.backend.Next) })
	controls := container.NewHBox(b.play, next)

	text := container.New(layout.NewVBoxLayout(), b.title, b.subtitle)
	row := container.NewBorder(nil, nil, b.cover.object, controls, container.NewCenter(text))

	// The tap/swipe catcher sits *below* the row: the buttons take their own
	// taps, and everything else (cover, labels, padding) falls through to it,
	// so the whole strip opens Now Playing without stealing the controls.
	opener := newTapArea(onOpen, onOpen)
	b.object = container.NewBorder(
		widget.NewSeparator(),
		b.progress,
		nil, nil,
		container.NewStack(opener, row),
	)
	return b
}

func (b *miniBar) Object() fyne.CanvasObject { return b.object }

func (b *miniBar) apply(st transportState) {
	applyPlayIcon(b.play, st.playing)
	b.title.SetText(st.title)
	b.subtitle.SetText(st.subtitle)
	if st.applyPosition {
		b.progress.setFraction(fraction(st.position, st.duration))
	}
}

func (b *miniBar) setCover(img image.Image) { b.cover.set(img) }

// applyPlayIcon flips a play/pause button to match the playback state.
func applyPlayIcon(btn *transportButton, playing bool) {
	if playing {
		btn.SetIcon(theme.MediaPauseIcon())
		return
	}
	btn.SetIcon(theme.MediaPlayIcon())
}

// applySeek pushes a snapshot into a seek slider and its time labels, honoring
// the player's "user is dragging" guard and the no-duration case (a slider with
// Max 0 would render a full bar).
func applySeek(seek *widget.Slider, pos, dur *widget.Label, st transportState) {
	if !st.applyPosition {
		return
	}
	if st.duration > 0 {
		seek.Max = st.duration
		seek.SetValue(st.position)
	} else {
		seek.Max = 1
		seek.SetValue(0)
	}
	pos.SetText(formatDuration(st.position))
	dur.SetText(formatDuration(st.duration))
}

// fraction is position/duration clamped to 0..1, for the mini bar's progress
// line (0 when the duration is unknown).
func fraction(position, duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	f := position / duration
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// thinProgress is a few pixels of non-interactive playback progress drawn under
// the mini bar. widget.ProgressBar is text-height and interactive-looking; this
// is a hairline that reads as "how far into the track" without inviting a drag
// that the bar is too narrow to make accurate.
type thinProgress struct {
	widget.BaseWidget
	fraction float64
}

const thinProgressHeight = 3

func newThinProgress() *thinProgress {
	p := &thinProgress{}
	p.ExtendBaseWidget(p)
	return p
}

func (p *thinProgress) setFraction(f float64) {
	if f == p.fraction {
		return
	}
	p.fraction = f
	p.Refresh()
}

func (p *thinProgress) MinSize() fyne.Size {
	return fyne.NewSize(0, thinProgressHeight)
}

func (p *thinProgress) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(theme.Color(theme.ColorNameInputBackground))
	fg := canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	return &thinProgressRenderer{bar: p, bg: bg, fg: fg}
}

type thinProgressRenderer struct {
	bar *thinProgress
	bg  *canvas.Rectangle
	fg  *canvas.Rectangle
}

func (r *thinProgressRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.fg.Resize(fyne.NewSize(size.Width*float32(r.bar.fraction), size.Height))
	r.fg.Move(fyne.NewPos(0, 0))
}

func (r *thinProgressRenderer) MinSize() fyne.Size { return r.bar.MinSize() }

func (r *thinProgressRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.fg}
}

func (r *thinProgressRenderer) Refresh() {
	r.bg.FillColor = theme.Color(theme.ColorNameInputBackground)
	r.fg.FillColor = theme.Color(theme.ColorNamePrimary)
	r.Layout(r.bar.Size())
	canvas.Refresh(r.bar)
}

func (r *thinProgressRenderer) Destroy() {}

// tapArea is an invisible widget that turns a tap, or a vertical swipe, into a
// callback. It is stacked under composite content so non-interactive children
// (labels, images) forward their pointer events to it while real controls in
// the same row keep theirs.
type tapArea struct {
	widget.BaseWidget
	onTap       func()
	onSwipeUp   func()
	onSwipeDown func()
	dragStart   fyne.Position
	dragging    bool
}

// tapAreaSwipe is how far the finger must travel vertically to count as a swipe.
const tapAreaSwipe = 30

func newTapArea(onTap, onSwipeUp func()) *tapArea {
	a := &tapArea{onTap: onTap, onSwipeUp: onSwipeUp}
	a.ExtendBaseWidget(a)
	return a
}

// newSwipeDownArea builds a tap area that fires on a downward swipe, used to
// dismiss a full-screen view with the gesture mirroring the one that opened it.
func newSwipeDownArea(onSwipeDown func()) *tapArea {
	a := &tapArea{onSwipeDown: onSwipeDown}
	a.ExtendBaseWidget(a)
	return a
}

func (a *tapArea) Tapped(_ *fyne.PointEvent) {
	if a.onTap != nil {
		a.onTap()
	}
}

func (a *tapArea) Dragged(e *fyne.DragEvent) {
	if !a.dragging {
		a.dragging = true
		a.dragStart = e.AbsolutePosition
	}
	travel := a.dragStart.Y - e.AbsolutePosition.Y
	switch {
	case a.onSwipeUp != nil && travel >= tapAreaSwipe:
		a.dragging = false
		a.onSwipeUp()
	case a.onSwipeDown != nil && -travel >= tapAreaSwipe:
		a.dragging = false
		a.onSwipeDown()
	}
}

func (a *tapArea) DragEnd() { a.dragging = false }

func (a *tapArea) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
