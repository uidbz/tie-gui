package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// openAlbum loads an album's tracks off the UI goroutine, then shows the track
// list. Playback actions arrive in Phase 2.
func (b *browsePage) openAlbum(a data.Album) {
	go func() {
		tracks, err := b.session.AlbumTracks(a)
		fyne.Do(func() { b.showAlbumView(a, tracks, err) })
	}()
}

// showBrowse restores the cover wall as the window content.
func (b *browsePage) showBrowse() {
	b.viewer.ChangeGallery()
}

// showAlbumView replaces the page content with an album header and track list.
func (b *browsePage) showAlbumView(a data.Album, tracks []data.Track, err error) {
	back := widget.NewButtonWithIcon("Albums", theme.NavigateBackIcon(), b.showBrowse)

	playBtn := widget.NewButtonWithIcon("Play album", theme.MediaPlayIcon(), func() {
		b.playTracks(tracks, 0)
	})
	playBtn.Importance = widget.HighImportance
	queueBtn := widget.NewButtonWithIcon("Add to playlist", theme.ContentAddIcon(), func() {
		b.enqueueTracks(tracks)
	})
	if err != nil || len(tracks) == 0 {
		playBtn.Disable()
		queueBtn.Disable()
	}

	title := widget.NewLabelWithStyle(a.Display(), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	// Album dirs carry no artist/year triple; fall back to the tracks' shared
	// artist so the header is useful.
	artist := a.Artist
	if artist == "" {
		artist = commonArtist(tracks)
	}
	subtitleText := artist
	if a.Year != "" {
		if subtitleText != "" {
			subtitleText += " · " + a.Year
		} else {
			subtitleText = a.Year
		}
	}
	subtitle := widget.NewLabel(subtitleText)

	header := container.NewVBox(
		container.NewHBox(back),
		title,
		subtitle,
		container.NewHBox(playBtn, queueBtn),
		widget.NewSeparator(),
	)

	var body fyne.CanvasObject
	var table *trackTable
	if err != nil {
		body = widget.NewLabel("Error loading tracks: " + err.Error())
	} else if len(tracks) == 0 {
		body = widget.NewLabel("No tracks found.")
	} else {
		table = newTrackTable(b.win, tracks, b.session.Cfg.AlbumColumns,
			// Play from the displayed (possibly re-sorted) order, so a tap starts
			// playback from that visible row onward.
			func(i int) { b.playTracks(table.tracks, i) },
			b.saveAlbumColumns,
			trackTableOpts{sortable: true, builtinColumnsButton: true},
		)
		body = table.object
	}

	b.win.SetContent(container.NewBorder(header, nil, nil, nil, body))
	if table != nil {
		table.show() // size the Title column now that the canvas width is known
	}
}

// saveAlbumColumns persists the album track-table's visible-column set/order to
// the app config so it survives across sessions.
func (b *browsePage) saveAlbumColumns(keys []string) {
	b.session.Cfg.AlbumColumns = keys
	if err := config.Save(b.session.Cfg); err != nil {
		dialog.ShowError(err, b.win)
	}
}

// saveQueueColumns persists the queue table's visible-column set/order,
// independently of the album columns.
func (b *browsePage) saveQueueColumns(keys []string) {
	b.session.Cfg.QueueColumns = keys
	if err := config.Save(b.session.Cfg); err != nil {
		dialog.ShowError(err, b.win)
	}
}

// streamable turns a slice of tracks into aligned (URL, track) lists, dropping
// tracks whose content hash has no configured filehost URL. The aligned tracks
// feed the transport's URL→Track registry so the queue can render full columns.
func (b *browsePage) streamable(tracks []data.Track) (urls []string, meta []data.Track) {
	for _, t := range tracks {
		url := b.session.StreamURL(t.Hash)
		if url == "" {
			continue
		}
		urls = append(urls, url)
		meta = append(meta, t)
	}
	return urls, meta
}

// playTracks replaces the queue with the album from index start onward and
// starts playback. Selecting a track plays from that track.
func (b *browsePage) playTracks(tracks []data.Track, start int) {
	if start < 0 || start >= len(tracks) {
		start = 0
	}
	urls, meta := b.streamable(tracks[start:])
	if len(urls) == 0 {
		b.reportPlaybackError(errors.New("no streamable tracks (is a filehost configured?)"))
		return
	}
	b.transport.SetQueue(urls, meta)
	go func() {
		if err := b.session.Backend.PlayAlbum(urls); err != nil {
			fyne.Do(func() { b.reportPlaybackError(err) })
		}
	}()
}

// enqueueTracks appends the album's tracks to the current queue.
func (b *browsePage) enqueueTracks(tracks []data.Track) {
	urls, meta := b.streamable(tracks)
	if len(urls) == 0 {
		b.reportPlaybackError(errors.New("no streamable tracks (is a filehost configured?)"))
		return
	}
	b.transport.AppendQueue(urls, meta)
	go func() {
		if err := b.session.Backend.Enqueue(urls...); err != nil {
			fyne.Do(func() { b.reportPlaybackError(err) })
		}
	}()
}

// albumInsertAt loads an album's tracks off the UI goroutine, then inserts them
// into the playlist at position gap (from a cover dragged onto the playlist pane).
func (b *browsePage) albumInsertAt(a data.Album, gap int) {
	go func() {
		tracks, err := b.session.AlbumTracks(a)
		fyne.Do(func() {
			if err != nil {
				b.reportPlaybackError(err)
				return
			}
			urls, meta := b.streamable(tracks)
			if len(urls) == 0 {
				b.reportPlaybackError(errors.New("no streamable tracks (is a filehost configured?)"))
				return
			}
			b.queue.insertTracksAt(gap, urls, meta)
		})
	}()
}

// reportPlaybackError surfaces a playback failure to the user.
func (b *browsePage) reportPlaybackError(err error) {
	dialog.ShowError(err, b.win)
}

// showAlbumMenu pops up Play/Add-to-queue actions over a cover-wall tile so an
// album can be played without opening its track list first.
func (b *browsePage) showAlbumMenu(tile *gallery.Tile, a data.Album) {
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Play album", func() { b.albumAction(a, true) }),
		fyne.NewMenuItem("Add to playlist", func() { b.albumAction(a, false) }),
	)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(tile)
	widget.ShowPopUpMenuAtPosition(menu, b.win.Canvas(), pos)
}

// albumAction loads an album's tracks off the UI goroutine, then plays or
// enqueues them.
func (b *browsePage) albumAction(a data.Album, play bool) {
	go func() {
		tracks, err := b.session.AlbumTracks(a)
		fyne.Do(func() {
			if err != nil {
				b.reportPlaybackError(err)
				return
			}
			if play {
				b.playTracks(tracks, 0)
			} else {
				b.enqueueTracks(tracks)
			}
		})
	}()
}

// commonArtist returns the artist shared by every track, or "" if they differ
// or none is set — used to label an album whose dir carries no artist triple.
func commonArtist(tracks []data.Track) string {
	artist := ""
	for _, t := range tracks {
		if t.Artist == "" {
			continue
		}
		if artist == "" {
			artist = t.Artist
		} else if artist != t.Artist {
			return ""
		}
	}
	return artist
}
