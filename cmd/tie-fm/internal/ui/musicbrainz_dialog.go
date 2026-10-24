package ui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

// musicBrainzDialog is the context-menu entry point for tie: rows: an album
// directory opens a release picker, an audio file a recording picker. A
// multi-file selection uses the shared parent album when the files live in the
// same directory.
func (fm *FileManager) musicBrainzDialog(entries []fs.Entry) {
	tc, err := fm.tieClient()
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	var dirs, files []fs.Entry
	for _, e := range entries {
		if !fs.IsTie(e.Path) || e.Hash == "" {
			continue
		}
		if e.IsDir {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}
	switch {
	case len(dirs) == 1 && len(files) == 0:
		fm.showAlbumTagDialog(tc, dirs[0])
	case len(dirs) == 0 && len(files) == 1:
		fm.showTrackTagDialog(tc, files[0])
	case len(dirs) == 0 && len(files) > 1:
		// Several tracks: treat them as their album, using the first file's
		// directory and album tags.
		fm.showAlbumTagDialogForFiles(tc, files)
	case len(dirs) > 1:
		dialog.ShowInformation("MusicBrainz",
			"Select a single album directory (or one audio file) to look up.", fm.win)
	default:
		dialog.ShowInformation("MusicBrainz",
			"Select an album directory or an audio file in a tie: path.", fm.win)
	}
}

// tieClient returns the tie client when the active pane is browsing tie:.
func (fm *FileManager) tieClient() (*client.TieClient, error) {
	backend := fm.registry.For("tie:/")
	provider, ok := backend.(interface{ Client() *client.TieClient })
	if !ok {
		return nil, fmt.Errorf("MusicBrainz tagging requires the tie backend")
	}
	tc := provider.Client()
	if tc == nil {
		return nil, fmt.Errorf("no tie client is connected")
	}
	return tc, nil
}

func (fm *FileManager) showAlbumTagDialog(tc *client.TieClient, e fs.Entry) {
	artist, album, year := tieAlbumFields(tc, e.Hash)
	q := musicbrainz.ReleaseQuery{Artist: artist, Album: album, Year: year}
	fm.showReleasePicker(q, nil, func(rel *musicbrainz.Release) {
		fm.applyReleaseToTieAlbum(tc, e.Path, rel)
	})
}

func (fm *FileManager) showAlbumTagDialogForFiles(tc *client.TieClient, files []fs.Entry) {
	e := files[0]
	_, artist, album, year := tieTrackFields(tc, e.Hash)
	dirPath := tieParentPath(e.Path)
	if album == "" {
		album = strings.TrimPrefix(strings.TrimPrefix(dirPath, "tie:"), "/")
	}
	q := musicbrainz.ReleaseQuery{Artist: artist, Album: album, Year: year}
	fm.showReleasePicker(q, nil, func(rel *musicbrainz.Release) {
		fm.applyReleaseToTieAlbum(tc, dirPath, rel)
	})
}

func (fm *FileManager) showTrackTagDialog(tc *client.TieClient, e fs.Entry) {
	title, artist, album, year := tieTrackFields(tc, e.Hash)
	_ = album
	_ = year
	q := musicbrainz.RecordingQuery{Artist: artist, Title: title}
	fm.showRecordingPicker(q, nil, func(rec *musicbrainz.Recording) {
		prog := busyDialog(fm.win, "Applying MusicBrainz tags…")
		go func() {
			err := applyRecordingToFile(tc, e.Hash, rec)
			fyne.Do(func() {
				prog.Hide()
				if err != nil {
					dialog.ShowError(err, fm.win)
					return
				}
				fm.reload()
				if fm.other != nil && fm.other.isTie() {
					fm.other.reload()
				}
			})
		}()
	})
}

func (fm *FileManager) applyReleaseToTieAlbum(tc *client.TieClient, dirURI string, rel *musicbrainz.Release) {
	if rel == nil {
		return
	}
	cfg := fm.cfg.MusicBrainz
	prog := busyDialog(fm.win, "Applying MusicBrainz tags…")
	go func() {
		// A picker result may come from a search (no tracks); fetch the full
		// release before writing track metadata.
		full := rel
		var err error
		if !rel.HasTracks() {
			p, perr := openMusicBrainz(cfg)
			if perr != nil {
				fyne.Do(func() { prog.Hide(); dialog.ShowError(perr, fm.win) })
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			full, err = p.LookupRelease(ctx, rel.MBID)
			cancel()
			p.Close()
		}
		if err == nil {
			err = applyReleaseToAlbum(tc, dirURI, full, cfg.FetchCoverArt, cfg)
		}
		fyne.Do(func() {
			prog.Hide()
			if err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
			fm.reload()
			if fm.other != nil && fm.other.isTie() {
				fm.other.reload()
			}
			dialog.ShowInformation("MusicBrainz", "Tags applied:\n"+musicBrainzNoticeText(full), fm.win)
		})
	}()
}

// tieAlbumFields reads album artist/album/year from a directory row.
func tieAlbumFields(tc *client.TieClient, uid string) (artist, album string, year int) {
	if uid == "" {
		return "", "", 0
	}
	row, err := tc.Get(uid)
	if err != nil {
		return "", "", 0
	}
	artist = client.RowFirst(row, "album-artist")
	if artist == "" {
		artist = client.RowFirst(row, "artist")
	}
	album = client.RowFirst(row, "album")
	if album == "" {
		album = client.RowFirst(row, "name")
	}
	year = parseRowInt(row, "year")
	return artist, album, year
}

// tieTrackFields reads title/artist/album/year from a file row.
func tieTrackFields(tc *client.TieClient, hash string) (title, artist, album string, year int) {
	if hash == "" {
		return "", "", "", 0
	}
	row, err := tc.Get(hash)
	if err != nil {
		return "", "", "", 0
	}
	title = client.RowFirst(row, "title")
	artist = client.RowFirst(row, "artist")
	album = client.RowFirst(row, "album")
	year = parseRowInt(row, "year")
	return title, artist, album, year
}

func tieParentPath(uri string) string {
	p := strings.TrimPrefix(uri, client.FileURIScheme)
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i > 0 {
		p = p[:i]
	} else {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return client.FileURIScheme + p
}

// --- shared pickers ---

type releaseListRow struct {
	title  *widget.Label
	detail *widget.Label
}

func busyDialog(win fyne.Window, text string) *dialog.CustomDialog {
	d := dialog.NewCustomWithoutButtons("MusicBrainz",
		container.NewVBox(widget.NewLabel(text), widget.NewProgressBarInfinite()), win)
	d.Show()
	return d
}

// showReleasePicker searches MusicBrainz releases and hands the chosen one to
// onPick. current, when non-nil, is shown as the existing match.
func showReleasePicker(win fyne.Window, settings musicbrainz.Settings, q musicbrainz.ReleaseQuery, current *musicbrainz.Release, onPick func(*musicbrainz.Release)) {
	p, err := musicbrainz.Open(settings)
	if err != nil {
		dialog.ShowError(err, win)
		return
	}
	closed := false
	closeProvider := func() {
		if !closed {
			closed = true
			p.Close()
		}
	}

	artist := widget.NewEntry()
	artist.SetText(q.Artist)
	artist.SetPlaceHolder("album artist")
	album := widget.NewEntry()
	album.SetText(q.Album)
	album.SetPlaceHolder("album title")

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var results []musicbrainz.Release
	selected := -1

	list := widget.NewList(
		func() int { return len(results) },
		func() fyne.CanvasObject {
			title := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			detail := widget.NewLabel("")
			detail.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(title, detail)
		},
		func(i int, o fyne.CanvasObject) {
			box := o.(*fyne.Container)
			title := box.Objects[0].(*widget.Label)
			detail := box.Objects[1].(*widget.Label)
			r := results[i]
			title.SetText(albumResultTitle(r))
			detail.SetText(albumResultDetail(r))
		})
	list.OnSelected = func(i int) { selected = i }
	list.OnUnselected = func(int) { selected = -1 }

	searchBtn := widget.NewButton("Search", nil)
	search := func() {
		query := q
		query.Artist = strings.TrimSpace(artist.Text)
		query.Album = strings.TrimSpace(album.Text)
		if query.Artist == "" && query.Album == "" {
			status.SetText("Enter an artist and/or album title.")
			return
		}
		query.Limit = 15
		status.SetText("Searching…")
		searchBtn.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			res, err := p.SearchReleases(ctx, query)
			cancel()
			fyne.Do(func() {
				searchBtn.Enable()
				if err != nil {
					status.SetText("Search failed: " + err.Error())
					return
				}
				results = res
				selected = -1
				list.UnselectAll()
				list.Refresh()
				switch {
				case len(res) == 0:
					status.SetText("No matching releases.")
				case len(res) == 1:
					selected = 0
					list.Select(0)
					status.SetText("1 release found.")
				default:
					status.SetText(fmt.Sprintf("%d releases found; pick one.", len(res)))
				}
			})
		}()
	}
	searchBtn.OnTapped = search

	details := widget.NewLabel("")
	details.Wrapping = fyne.TextWrapWord
	if current != nil {
		details.SetText("Current: " + musicBrainzNoticeText(current))
	}

	spacer := newSpacer(620, 300)
	content := container.NewBorder(
		container.NewVBox(
			widget.NewForm(
				widget.NewFormItem("Artist", artist),
				widget.NewFormItem("Album", album),
			),
			container.NewHBox(searchBtn, status),
			details,
			widget.NewSeparator(),
		),
		nil, nil, nil,
		container.NewStack(spacer, list),
	)

	d := dialog.NewCustomConfirm("MusicBrainz release", "Use release", "Cancel", content, func(ok bool) {
		if !ok {
			closeProvider()
			return
		}
		if selected < 0 || selected >= len(results) {
			if len(results) == 0 {
				dialog.ShowInformation("MusicBrainz", "Search first, then pick a release.", win)
				return
			}
			selected = 0
		}
		pick := results[selected]
		if pick.HasTracks() {
			closeProvider()
			onPick(&pick)
			return
		}
		prog := busyDialog(win, "Loading release…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			full, err := p.LookupRelease(ctx, pick.MBID)
			cancel()
			closeProvider()
			fyne.Do(func() {
				prog.Hide()
				if err != nil {
					dialog.ShowError(err, win)
					return
				}
				onPick(full)
			})
		}()
	}, win)
	d.Resize(fyne.NewSize(700, 560))
	d.Show()
	search()
}

// showRecordingPicker searches recordings and hands the chosen one to onPick.
func showRecordingPicker(win fyne.Window, settings musicbrainz.Settings, q musicbrainz.RecordingQuery, current *musicbrainz.Recording, onPick func(*musicbrainz.Recording)) {
	p, err := musicbrainz.Open(settings)
	if err != nil {
		dialog.ShowError(err, win)
		return
	}
	closed := false
	closeProvider := func() {
		if !closed {
			closed = true
			p.Close()
		}
	}

	artist := widget.NewEntry()
	artist.SetText(q.Artist)
	artist.SetPlaceHolder("artist")
	title := widget.NewEntry()
	title.SetText(q.Title)
	title.SetPlaceHolder("track title")

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var results []musicbrainz.Recording
	selected := -1

	list := widget.NewList(
		func() int { return len(results) },
		func() fyne.CanvasObject {
			t := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			d := widget.NewLabel("")
			d.Truncation = fyne.TextTruncateEllipsis
			return container.NewVBox(t, d)
		},
		func(i int, o fyne.CanvasObject) {
			box := o.(*fyne.Container)
			t := box.Objects[0].(*widget.Label)
			d := box.Objects[1].(*widget.Label)
			r := results[i]
			t.SetText(r.Title)
			detail := r.ArtistCredit
			if detail == "" {
				detail = r.Artist
			}
			if r.LengthMS > 0 {
				detail += " · " + formatDurationMS(r.LengthMS)
			}
			if len(r.Releases) > 0 {
				detail += " · " + r.Releases[0].Title
			}
			d.SetText(detail)
		})
	list.OnSelected = func(i int) { selected = i }
	list.OnUnselected = func(int) { selected = -1 }

	searchBtn := widget.NewButton("Search", nil)
	search := func() {
		query := q
		query.Artist = strings.TrimSpace(artist.Text)
		query.Title = strings.TrimSpace(title.Text)
		if query.Artist == "" && query.Title == "" {
			status.SetText("Enter an artist and/or track title.")
			return
		}
		query.Limit = 15
		status.SetText("Searching…")
		searchBtn.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			res, err := p.SearchRecordings(ctx, query)
			cancel()
			fyne.Do(func() {
				searchBtn.Enable()
				if err != nil {
					status.SetText("Search failed: " + err.Error())
					return
				}
				results = res
				selected = -1
				list.UnselectAll()
				list.Refresh()
				if len(res) == 0 {
					status.SetText("No matching recordings.")
				} else {
					selected = 0
					list.Select(0)
					status.SetText(fmt.Sprintf("%d recordings found.", len(res)))
				}
			})
		}()
	}
	searchBtn.OnTapped = search

	var currentLabel *widget.Label
	if current != nil {
		currentLabel = widget.NewLabel("Current: " + current.Title)
	}

	top := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Artist", artist),
			widget.NewFormItem("Title", title),
		),
		container.NewHBox(searchBtn, status),
	)
	if currentLabel != nil {
		top.Add(currentLabel)
	}
	top.Add(widget.NewSeparator())

	content := container.NewBorder(top, nil, nil, nil,
		container.NewStack(newSpacer(620, 280), list))

	d := dialog.NewCustomConfirm("MusicBrainz recording", "Use recording", "Cancel", content, func(ok bool) {
		defer closeProvider()
		if !ok {
			return
		}
		if len(results) == 0 {
			dialog.ShowInformation("MusicBrainz", "Search first, then pick a recording.", win)
			return
		}
		if selected < 0 || selected >= len(results) {
			selected = 0
		}
		rec := results[selected]
		onPick(&rec)
	}, win)
	d.Resize(fyne.NewSize(680, 540))
	d.Show()
	search()
}

func albumResultTitle(r musicbrainz.Release) string {
	artist := r.DisplayArtist()
	if artist == "" {
		artist = "Unknown artist"
	}
	title := r.Title
	if title == "" {
		title = "(untitled)"
	}
	out := artist + " — " + title
	if r.Year > 0 {
		out += fmt.Sprintf(" (%d)", r.Year)
	}
	return out
}

func albumResultDetail(r musicbrainz.Release) string {
	var parts []string
	if r.PrimaryType != "" {
		parts = append(parts, r.PrimaryType)
	}
	if len(r.SecondaryTypes) > 0 {
		parts = append(parts, strings.Join(r.SecondaryTypes, ", "))
	}
	if r.TrackCount > 0 {
		parts = append(parts, fmt.Sprintf("%d tracks", r.TrackCount))
	}
	if r.DiscCount > 1 {
		parts = append(parts, fmt.Sprintf("%d discs", r.DiscCount))
	}
	if r.Country != "" {
		parts = append(parts, r.Country)
	}
	if r.Status != "" {
		parts = append(parts, r.Status)
	}
	if r.MBID != "" {
		parts = append(parts, r.MBID)
	}
	return strings.Join(parts, " · ")
}

func formatDurationMS(ms int) string {
	if ms <= 0 {
		return "?"
	}
	total := ms / 1000
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// newSpacer returns a transparent canvas object that gives a bare list a
// usable minimum size in a dialog (mirrors the album import plan dialog).
func newSpacer(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

// --- FileManager wrappers (configured backend) ---

func (fm *FileManager) showReleasePicker(q musicbrainz.ReleaseQuery, current *musicbrainz.Release, onPick func(*musicbrainz.Release)) {
	showReleasePicker(fm.win, musicBrainzSettings(fm.cfg.MusicBrainz), q, current, onPick)
}

func (fm *FileManager) showRecordingPicker(q musicbrainz.RecordingQuery, current *musicbrainz.Recording, onPick func(*musicbrainz.Recording)) {
	showRecordingPicker(fm.win, musicBrainzSettings(fm.cfg.MusicBrainz), q, current, onPick)
}
