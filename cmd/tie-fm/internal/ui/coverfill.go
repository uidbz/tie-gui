package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/disintegration/imaging"
	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/tiethumb"
)

// fillMissingCovers offers the bulk artwork pass for tie albums: local
// candidates first (folder images, subfolders, embedded tracks, zip members),
// then MusicBrainz/Cover Art Archive when a release match is available.
func (fm *FileManager) fillMissingCovers(entries []fs.Entry) {
	tc, err := fm.tieClient()
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	var selected []string
	for _, e := range entries {
		if fs.IsTie(e.Path) && e.IsDir && e.Hash != "" {
			selected = append(selected, e.Hash)
		}
	}
	if len(selected) == 0 {
		dialog.ShowInformation("Missing covers", "Select one or more tie album directories.", fm.win)
		return
	}

	scope := widget.NewRadioGroup([]string{"Selected folders", "All audio albums"}, nil)
	scope.SetSelected("Selected folders")
	embedded := widget.NewCheck("Include embedded artwork", nil)
	embedded.SetChecked(true)
	archives := widget.NewCheck("Include audio archives", nil)
	archives.SetChecked(true)
	onlyMissing := widget.NewCheck("Only albums without cover art", nil)
	onlyMissing.SetChecked(true)
	online := widget.NewCheck("Look up missing covers on MusicBrainz", nil)
	online.SetChecked(fm.cfg.MusicBrainz.FetchCoverArt)

	form := dialog.NewForm("Fill missing covers", "Start", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Scope", scope),
		widget.NewFormItem("", onlyMissing),
		widget.NewFormItem("", embedded),
		widget.NewFormItem("", archives),
		widget.NewFormItem("", online),
	}, func(ok bool) {
		if !ok {
			return
		}
		uids := selected
		if scope.Selected == "All audio albums" {
			var err error
			uids, err = allCoverAlbums(tc, archives.Checked)
			if err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
		}
		if len(uids) == 0 {
			dialog.ShowInformation("Missing covers", "No albums found.", fm.win)
			return
		}
		fm.runCoverFill(tc, uids, coverFillOptions{
			embedded: embedded.Checked,
			archives: archives.Checked,
			online:   online.Checked,
			missing:  onlyMissing.Checked,
		})
	}, fm.win)
	form.Resize(fyne.NewSize(420, 0))
	form.Show()
}

type coverFillOptions struct {
	embedded bool
	archives bool
	online   bool
	missing  bool
}

func (fm *FileManager) runCoverFill(tc *client.TieClient, uids []string, opts coverFillOptions) {
	label := widget.NewLabel("Resolving covers…")
	prog := dialog.NewCustomWithoutButtons("Missing covers",
		container.NewVBox(label, widget.NewProgressBarInfinite()), fm.win)
	prog.Show()

	go func() {
		var local, online, skipped, none int
		var failures []string
		var mb musicbrainz.Provider
		closeMB := func() {
			if mb != nil {
				mb.Close()
				mb = nil
			}
		}
		defer closeMB()

		for i, uid := range uids {
			if opts.missing && albumHasLocalArt(tc, client.DirUID(uid)) {
				skipped++
				continue
			}
			fyne.Do(func() {
				label.SetText(fmt.Sprintf("Resolving covers %d/%d…", i+1, len(uids)))
			})

			var low *client.CoverResult
			res, err := client.ResolveCover(tc, uid, client.CoverOptions{
				Host:            tiethumb.FileHost(tc),
				IncludeParent:   true,
				IncludeEmbedded: opts.embedded,
				IncludeArchives: opts.archives,
			})
			switch {
			case err == nil && res.Confidence != client.CoverLow:
				if err := thumbnailFromCover(tc, uid, res); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", uid, err))
					continue
				}
				local++
				continue
			case err == nil:
				low = &res // last resort; an online front may still replace it
			case !errors.Is(err, client.ErrNoCover):
				failures = append(failures, fmt.Sprintf("%s: %v", uid, err))
				continue
			}

			useLow := func() {
				if low == nil {
					none++
					return
				}
				if err := thumbnailFromCover(tc, uid, *low); err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", uid, err))
					return
				}
				local++
			}
			if !opts.online {
				useLow()
				continue
			}

			if mb == nil {
				mb, err = openMusicBrainz(fm.cfg.MusicBrainz)
				if err != nil {
					failures = append(failures, "MusicBrainz: "+err.Error())
					opts.online = false
					useLow()
					continue
				}
			}
			q := albumReleaseQuery(tc, uid)
			if q.Album == "" && q.Artist == "" {
				useLow()
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			rels, err := mb.SearchReleases(ctx, q)
			cancel()
			if err != nil || len(rels) == 0 || rels[0].Score < 0.55 {
				useLow()
				continue
			}
			ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
			full, err := mb.LookupRelease(ctx, rels[0].MBID)
			cancel()
			if err != nil || full == nil || !full.HasCoverArt {
				useLow()
				continue
			}
			if err := ensureAlbumCoverArt(tc, client.DirUID(uid), full, fm.cfg.MusicBrainz); err != nil {
				useLow()
				continue
			}
			online++
		}
		closeMB()

		fyne.Do(func() {
			prog.Hide()
			fm.reload()
			if fm.other != nil && fm.other.isTie() {
				fm.other.reload()
			}
			msg := fmt.Sprintf("Local artwork: %d\nMusicBrainz: %d\nAlready had artwork: %d\nStill without cover: %d",
				local, online, skipped, none)
			if len(failures) > 0 {
				msg += fmt.Sprintf("\nErrors: %d", len(failures))
				dialog.ShowError(fmt.Errorf("%s\n\n%s", msg, strings.Join(failures, "\n")), fm.win)
				return
			}
			dialog.ShowInformation("Missing covers", msg, fm.win)
		})
	}()
}

// thumbnailFromCover generates the small display thumbnail for the source
// artwork ResolveCover recorded.
func thumbnailFromCover(tc *client.TieClient, uid string, res client.CoverResult) error {
	host := tiethumb.FileHost(tc)
	data := res.Data
	if len(data) == 0 {
		var err error
		data, err = tiethumb.FetchBlob(host, res.Hash)
		if err != nil {
			return err
		}
	}
	img, _, err := gallery.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode cover: %w", err)
	}
	if b := img.Bounds(); b.Dx() > 512 || b.Dy() > 512 {
		img = imaging.Fit(img, 512, 512, imaging.Lanczos)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return err
	}
	_, err = tiethumb.UploadThumbnail(tc, host, uid, buf.Bytes())
	return err
}

// allCoverAlbums lists the collection's audio-dir (and optionally
// audio-archive) subjects for a whole-collection fill.
func allCoverAlbums(tc *client.TieClient, archives bool) ([]string, error) {
	types := []string{client.TieAudioDir.String()}
	if archives {
		types = append(types, client.TieAudioArchive.String())
	}
	var out []string
	seen := map[string]bool{}
	for _, typ := range types {
		rows, _, err := tc.Query(client.QuerySpec{
			Terms: []string{typ}, Filter: string(client.RelType), Reverse: true,
			Expand: true, Limit: -1,
		})
		if errors.Is(err, client.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if !seen[row.Key] {
				seen[row.Key] = true
				out = append(out, row.Key)
			}
		}
	}
	return out, nil
}

// albumReleaseQuery builds a MusicBrainz release query from an album row,
// falling back to its first audio track when the directory carries no tags.
func albumReleaseQuery(tc *client.TieClient, uid string) musicbrainz.ReleaseQuery {
	q := musicbrainz.ReleaseQuery{Limit: 5}
	if row, err := tc.Get(uid); err == nil {
		q.Artist = client.RowFirst(row, client.RelAlbumArtist)
		if q.Artist == "" {
			q.Artist = client.RowFirst(row, client.RelArtist)
		}
		q.Album = client.RowFirst(row, client.RelAlbum)
		if q.Album == "" {
			q.Album = client.RowFirst(row, client.RelName)
		}
		q.Year, _ = strconv.Atoi(client.RowFirst(row, client.RelYear))
		q.TrackCount, _ = strconv.Atoi(client.RowFirst(row, client.RelTrackCount))
		if q.Artist != "" && q.Album != "" {
			return q
		}
	}
	// First audio file below the album carries the cleanest tags.
	dir, err := client.ReadTieDir(tc, client.DirUID(uid))
	if err != nil {
		return q
	}
	var hashes []string
	var walk func(client.Directory, int)
	walk = func(d client.Directory, depth int) {
		for _, f := range d.Files {
			if f.TieType == client.TieAudioFile || strings.HasPrefix(f.MediaType, "audio/") {
				hashes = append(hashes, f.Uid)
			}
		}
		if depth >= 2 {
			return
		}
		for _, sub := range d.SubDirs {
			sd, err := client.ReadTieDir(tc, sub.Uid)
			if err == nil {
				walk(sd, depth+1)
			}
		}
	}
	walk(dir, 0)
	if len(hashes) == 0 {
		return q
	}
	if rows, err := tc.Expand(hashes[:min(3, len(hashes))]); err == nil {
		for _, row := range rows {
			if q.Artist == "" {
				q.Artist = client.RowFirst(row, client.RelAlbumArtist)
				if q.Artist == "" {
					q.Artist = client.RowFirst(row, client.RelArtist)
				}
			}
			if q.Album == "" {
				q.Album = client.RowFirst(row, client.RelAlbum)
			}
			if q.Year == 0 {
				q.Year, _ = strconv.Atoi(client.RowFirst(row, client.RelYear))
			}
		}
	}
	return q
}

// useAsAlbumCover makes a selected tie image the source cover of the nearest
// audio-dir ancestor, then clears the thumbnail so it regenerates.
func (fm *FileManager) useAsAlbumCover(e fs.Entry) {
	tc, err := fm.tieClient()
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	row, err := tc.Get(e.Hash)
	if err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	if !slices.Contains(client.RowValues(row, client.RelType), client.TieImageFile.String()) {
		dialog.ShowInformation("Album cover", "Select an image file.", fm.win)
		return
	}
	album := albumForImage(tc, e.Hash)
	if album == "" {
		dialog.ShowInformation("Album cover", "This image is not inside an audio album.", fm.win)
		return
	}
	source := "manual:" + client.RowFirst(row, client.RelFilename)
	if err := client.SetCover(tc, album, e.Hash, source); err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	if err := tc.Set(album, string(client.RelThumbnail), nil); err != nil {
		dialog.ShowError(err, fm.win)
		return
	}
	fm.reload()
	if fm.other != nil && fm.other.isTie() {
		fm.other.reload()
	}
	dialog.ShowInformation("Album cover", "The image is now the album cover.", fm.win)
}

func isImageEntry(e fs.Entry) bool {
	switch strings.ToLower(filepath.Ext(e.Name)) {
	case ".jpg", ".jpeg", ".jfif", ".png", ".gif", ".webp", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

func albumForImage(tc *client.TieClient, hash string) string {
	row, err := tc.Get(hash)
	if err != nil {
		return ""
	}
	parents := client.RowValues(row, client.RelParent)
	for _, parent := range parents {
		for depth := 0; depth < 8 && parent != ""; depth++ {
			prow, err := tc.Get(parent)
			if err != nil {
				break
			}
			types := client.RowValues(prow, client.RelType)
			if slices.Contains(types, client.TieAudioDir.String()) ||
				slices.Contains(types, client.TieAudioArchive.String()) {
				return parent
			}
			parent = client.RowFirst(prow, client.RelParent)
		}
	}
	return ""
}
