package ui

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// desktopNowPlaying is the desktop wall's "Now Playing" view: a large cover of
// the current track with its title and artist/album. Playback controls stay on
// the regular transport bar pinned below, so this view deliberately carries no
// second set of buttons or sliders.
type desktopNowPlaying struct {
	object   fyne.CanvasObject
	cover    *coverView
	title    *widget.Label
	subtitle *widget.Label
}

func newDesktopNowPlaying() *desktopNowPlaying {
	v := &desktopNowPlaying{
		cover:    newCoverView(0),
		title:    widget.NewLabelWithStyle("Nothing playing", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		subtitle: widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{}),
	}
	v.title.Truncation = fyne.TextTruncateEllipsis
	v.subtitle.Truncation = fyne.TextTruncateEllipsis

	info := container.NewVBox(
		widget.NewIcon(theme.MediaMusicIcon()),
		v.title,
		v.subtitle,
	)
	body := container.NewBorder(nil, container.NewPadded(info), nil, nil,
		container.NewPadded(v.cover.object))
	v.object = body
	return v
}

func (v *desktopNowPlaying) Object() fyne.CanvasObject { return v.object }

func (v *desktopNowPlaying) apply(st transportState) {
	v.title.SetText(st.title)
	v.subtitle.SetText(st.subtitle)
}

func (v *desktopNowPlaying) setCover(img image.Image) { v.cover.set(img) }
