package ui

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// nowPlayingButton is the edge of the square each secondary transport button
// occupies on the Now Playing page: finger-sized, unlike the icon-sized
// buttons in a bar. Play/pause is larger (nowPlayingPlay) so it stands out.
const nowPlayingButton = 56

// nowPlayingPage is the compact layout's full-screen player: a large cover, the
// track metadata, and — the point of the page — a seek slider and a volume
// slider that each span the whole screen width. On a phone those two controls
// are unusable squeezed into a bar beside the transport buttons, which is the
// pain point this page exists to fix.
//
// The page is a transportView like the bars, so it stays in step with the
// player whether it is on screen or not; the shell simply swaps the window
// content to it.
type nowPlayingPage struct {
	object fyne.CanvasObject

	cover    *coverView
	play     *transportButton
	seek     *widget.Slider
	volume   *widget.Slider
	pos      *widget.Label
	dur      *widget.Label
	title    *widget.Label
	subtitle *widget.Label
}

// newNowPlayingPage builds the page. back is invoked by its Back button (and by
// the shell for the Android back key / edge swipe).
func newNowPlayingPage(p *player, back func()) *nowPlayingPage {
	n := &nowPlayingPage{
		cover:    newCoverView(0), // fills the page's center
		seek:     p.newSeekSlider(),
		volume:   p.newVolumeSlider(),
		pos:      widget.NewLabel("0:00"),
		dur:      widget.NewLabel("0:00"),
		title:    widget.NewLabelWithStyle("Nothing playing", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		subtitle: widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{}),
	}
	n.title.Truncation = fyne.TextTruncateEllipsis
	n.subtitle.Truncation = fyne.TextTruncateEllipsis

	backBtn := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), back)
	backBtn.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, backBtn, nil,
		widget.NewLabelWithStyle("Now playing", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}))

	prev := newTransportButton(theme.MediaSkipPreviousIcon(), nowPlayingButton, false, func() { p.do(p.backend.Previous) })
	n.play = newTransportButton(theme.MediaPlayIcon(), nowPlayingPlay, true, p.togglePlay)
	next := newTransportButton(theme.MediaSkipNextIcon(), nowPlayingButton, false, func() { p.do(p.backend.Next) })
	stop := newTransportButton(theme.MediaStopIcon(), nowPlayingButton, false, func() { p.do(p.backend.Stop) })
	controls := container.NewCenter(container.New(
		layout.NewCustomPaddedHBoxLayout(12),
		prev,
		n.play,
		next,
		stop,
	))

	// Both sliders get the full width: the seek times go *under* the slider
	// rather than beside it, and the volume icon is the only thing sharing the
	// volume row.
	times := container.NewBorder(nil, nil, n.pos, n.dur, nil)
	seekBox := container.NewVBox(n.seek, times)
	volBox := container.NewBorder(nil, nil, widget.NewIcon(theme.VolumeUpIcon()), nil, n.volume)

	bottom := container.NewVBox(
		n.title,
		n.subtitle,
		seekBox,
		controls,
		volBox,
	)

	// A swipe down over the cover leaves the page, mirroring the swipe up that
	// opened it from the mini bar.
	coverArea := container.NewStack(newSwipeDownArea(back), container.NewPadded(n.cover.object))

	n.object = container.NewBorder(header, bottom, nil, nil, coverArea)
	return n
}

func (n *nowPlayingPage) Object() fyne.CanvasObject { return n.object }

func (n *nowPlayingPage) apply(st transportState) {
	applyPlayIcon(n.play, st.playing)
	n.title.SetText(st.title)
	n.subtitle.SetText(st.subtitle)
	applySeek(n.seek, n.pos, n.dur, st)
	if st.applyVolume {
		n.volume.SetValue(st.volume)
	}
}

func (n *nowPlayingPage) setCover(img image.Image) { n.cover.set(img) }
