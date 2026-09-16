package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Transport control button sizes: the edge of each button's square hit area.
const (
	transportBarButton = 36 // prev/next/stop in the desktop transport bar
	transportBarPlay   = 42 // play/pause in the desktop transport bar
	nowPlayingButton   = 56 // prev/next/stop on the Now Playing page and the mini bar
	nowPlayingPlay     = 72 // play/pause on the Now Playing page and the mini bar
)

// Icon edge as a fraction of the button edge. The primary disc leaves more
// air around its icon; the flat buttons are all icon.
const (
	transportIconRatioPrimary = 0.50
	transportIconRatioFlat    = 0.56
)

// transportButton is a round icon button for the playback controls. The
// primary style is a filled disc in the theme's primary color with a
// contrasting icon — the play/pause button, the control the eye should land
// on first. The flat style is a bare themed icon that gains a translucent
// disc on hover (desktop) and while pressed, keeping the secondary controls
// quiet until they are touched. Taps get a short ripple fade on either
// style, mirroring the standard button's tap animation.
type transportButton struct {
	widget.BaseWidget
	onTap     func()
	primary   bool          // filled-disc style; flat when false
	size      float32       // edge of the square hit area
	source    fyne.Resource // untinted icon; the renderer tints it per style
	hovered   bool
	pressFade float32 // 1 on tap, fades to 0 — the ripple
	anim      *fyne.Animation
}

// newTransportButton builds a round transport button: primary for the
// filled-disc look (play/pause), flat for the quiet icon look.
func newTransportButton(icon fyne.Resource, size float32, primary bool, onTap func()) *transportButton {
	b := &transportButton{onTap: onTap, primary: primary, size: size, source: icon}
	b.ExtendBaseWidget(b)
	return b
}

// SetIcon swaps the button's icon (the play/pause flip).
func (b *transportButton) SetIcon(res fyne.Resource) {
	b.source = res
	b.Refresh()
}

// MinSize is a square of the button's edge.
func (b *transportButton) MinSize() fyne.Size {
	return fyne.NewSize(b.size, b.size)
}

// Tapped implements fyne.Tappable: ripple feedback, then the action.
func (b *transportButton) Tapped(_ *fyne.PointEvent) {
	b.ripple()
	if b.onTap != nil {
		b.onTap()
	}
}

// ripple flashes the press overlay and fades it back out — the round
// equivalent of the standard button's tap animation.
func (b *transportButton) ripple() {
	if b.anim != nil {
		b.anim.Stop()
	}
	if !fyne.CurrentApp().Settings().ShowAnimations() {
		return
	}
	b.pressFade = 1
	b.Refresh()
	b.anim = fyne.NewAnimation(canvas.DurationShort, func(done float32) {
		b.pressFade = 1 - done
		b.Refresh()
	})
	b.anim.Curve = fyne.AnimationEaseOut
	b.anim.Start()
}

// MouseIn/MouseMoved/MouseOut implement desktop.Hoverable: a subtle disc
// appears under the pointer on the desktop.
func (b *transportButton) MouseIn(_ *desktop.MouseEvent) {
	b.hovered = true
	b.Refresh()
}

func (b *transportButton) MouseMoved(_ *desktop.MouseEvent) {}

func (b *transportButton) MouseOut() {
	b.hovered = false
	b.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (b *transportButton) CreateRenderer() fyne.WidgetRenderer {
	r := &transportButtonRenderer{
		button:  b,
		disc:    canvas.NewCircle(color.Transparent),
		overlay: canvas.NewCircle(color.Transparent),
		icon:    canvas.NewImageFromResource(b.tintedIcon()),
	}
	r.icon.FillMode = canvas.ImageFillContain
	r.icon.ScaleMode = canvas.ImageScaleSmooth
	r.Refresh()
	return r
}

// tintedIcon wraps the source icon in the theme color matching the button's
// style: contrasting on the primary disc, plain foreground when flat.
func (b *transportButton) tintedIcon() fyne.Resource {
	if b.primary {
		return theme.NewColoredResource(b.source, theme.ColorNameForegroundOnPrimary)
	}
	return theme.NewThemedResource(b.source)
}

// transportButtonRenderer draws the disc, the hover/press overlay disc, and
// the centered icon.
type transportButtonRenderer struct {
	button  *transportButton
	disc    *canvas.Circle
	overlay *canvas.Circle
	icon    *canvas.Image
}

func (r *transportButtonRenderer) Layout(size fyne.Size) {
	r.disc.Resize(size)
	r.overlay.Resize(size)
	ratio := float32(transportIconRatioFlat)
	if r.button.primary {
		ratio = transportIconRatioPrimary
	}
	edge := size.Width * ratio
	r.icon.Resize(fyne.NewSize(edge, edge))
	r.icon.Move(fyne.NewPos((size.Width-edge)/2, (size.Height-edge)/2))
}

func (r *transportButtonRenderer) MinSize() fyne.Size { return r.button.MinSize() }

func (r *transportButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.disc, r.overlay, r.icon}
}

func (r *transportButtonRenderer) Refresh() {
	b := r.button
	if b.primary {
		r.disc.FillColor = theme.Color(theme.ColorNamePrimary)
	} else {
		r.disc.FillColor = color.Transparent
	}
	switch {
	case b.pressFade > 0:
		pressed, _ := color.NRGBAModel.Convert(theme.Color(theme.ColorNamePressed)).(color.NRGBA)
		pressed.A = uint8(float32(pressed.A) * b.pressFade)
		r.overlay.FillColor = pressed
	case b.hovered:
		r.overlay.FillColor = theme.Color(theme.ColorNameHover)
	default:
		r.overlay.FillColor = color.Transparent
	}
	r.icon.Resource = b.tintedIcon()
	r.icon.Refresh()
	r.disc.Refresh()
	r.overlay.Refresh()
}

func (r *transportButtonRenderer) Destroy() {}
