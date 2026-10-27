package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

// albumTagTarget is one album directory the tag panel's MusicBrainz action can
// tag. uri is the directory's tie: path; uid its DirUID.
type albumTagTarget struct {
	uri    string
	uid    client.DirUID
	artist string
	album  string
	year   int
}

// onMusicBrainzTags is the tag panel's "Add MusicBrainz tags…" action: it adds
// only the release's genres/community tags to the selected album(s), leaving
// artist/album/year, MBIDs, track metadata and cover art untouched.
func (tp *TagPanel) onMusicBrainzTags() {
	if !tp.fm.isTie() || len(tp.loadedEntries) == 0 {
		return
	}
	tc, err := tp.fm.tieClient()
	if err != nil {
		dialog.ShowError(err, tp.fm.win)
		return
	}
	targets := albumTagTargets(tc, tp.loadedEntries)
	if len(targets) == 0 {
		dialog.ShowInformation("MusicBrainz",
			"Select an album directory (or tracks inside one) to add MusicBrainz tags.", tp.fm.win)
		return
	}
	if len(targets) == 1 {
		tp.fm.pickMusicBrainzTags(tc, targets[0])
		return
	}
	tp.fm.autoMusicBrainzTags(tc, targets)
}

// albumTagTargets resolves the selected entries to the album directories whose
// tags they edit: a directory is its own target, a file maps to its parent
// album directory. Duplicates are dropped.
func albumTagTargets(tc *client.TieClient, entries []fs.Entry) []albumTagTarget {
	seen := map[client.DirUID]bool{}
	var out []albumTagTarget
	add := func(uri string, uid client.DirUID) {
		if uid == "" || seen[uid] {
			return
		}
		seen[uid] = true
		artist, album, year := tieAlbumFields(tc, string(uid))
		out = append(out, albumTagTarget{uri: uri, uid: uid, artist: artist, album: album, year: year})
	}
	for _, e := range entries {
		if e.Hash == "" {
			continue
		}
		if e.IsDir {
			add(e.Path, client.DirUID(e.Hash))
			continue
		}
		parent := tieParentPath(e.Path)
		if uid, err := tc.DirUIDFromPath(parent); err == nil {
			add(parent, uid)
		}
	}
	return out
}

// pickMusicBrainzTags lets the user choose the release for one album, then adds
// its tags.
func (fm *FileManager) pickMusicBrainzTags(tc *client.TieClient, t albumTagTarget) {
	q := musicbrainz.ReleaseQuery{Artist: t.artist, Album: t.album, Year: t.year}
	fm.showReleasePicker(q, nil, func(rel *musicbrainz.Release) {
		prog := busyDialog(fm.win, "Adding MusicBrainz tags…")
		go func() {
			added, err := addReleaseTagsOnly(tc, t.uid, rel)
			fyne.Do(func() {
				prog.Hide()
				if err != nil {
					dialog.ShowError(err, fm.win)
					return
				}
				fm.afterMusicBrainzTags(tc, []albumTagTarget{t}, added, rel, nil)
			})
		}()
	})
}

// autoMusicBrainzTags handles a multi-selection: every album is matched
// automatically, and its tags are added. Unmatched/failed albums are reported.
func (fm *FileManager) autoMusicBrainzTags(tc *client.TieClient, targets []albumTagTarget) {
	cfg := fm.cfg.MusicBrainz
	label := widget.NewLabel("Adding MusicBrainz tags…")
	prog := dialog.NewCustomWithoutButtons("MusicBrainz",
		container.NewVBox(label, widget.NewProgressBarInfinite()), fm.win)
	prog.Show()
	go func() {
		var (
			addedAlbums int
			unmatched   []string
			failed      []string
			lastRel     *musicbrainz.Release
		)
		p, err := openMusicBrainz(cfg)
		if err != nil {
			fyne.Do(func() {
				prog.Hide()
				dialog.ShowError(err, fm.win)
			})
			return
		}
		defer p.Close()
		for i, t := range targets {
			current := t.album
			if current == "" {
				current = t.uri
			}
			fyne.Do(func() {
				label.SetText(fmt.Sprintf("MusicBrainz tags %d/%d — %s", i+1, len(targets), current))
			})
			q := musicbrainz.ReleaseQuery{
				Artist: t.artist,
				Album:  t.album,
				Year:   t.year,
				Limit:  5,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			rels, serr := p.SearchReleases(ctx, q)
			cancel()
			if serr != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", current, serr))
				continue
			}
			if len(rels) == 0 || rels[0].Score < 0.55 {
				unmatched = append(unmatched, current)
				continue
			}
			ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
			full, lerr := p.LookupRelease(ctx, rels[0].MBID)
			cancel()
			if lerr != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", current, lerr))
				continue
			}
			if _, aerr := addReleaseTagsOnly(tc, t.uid, full); aerr != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", current, aerr))
				continue
			}
			addedAlbums++
			lastRel = full
		}
		fyne.Do(func() {
			prog.Hide()
			fm.afterMusicBrainzTags(tc, targets, nil, lastRel, map[string]any{
				"added":     addedAlbums,
				"unmatched": unmatched,
				"failed":    failed,
			})
		})
	}()
}

// afterMusicBrainzTags refreshes the listing and tag panel after a tag-only
// write. single is set for the interactive one-album path; summary for the
// automatic multi-album path.
func (fm *FileManager) afterMusicBrainzTags(tc *client.TieClient, targets []albumTagTarget, added []string, rel *musicbrainz.Release, summary map[string]any) {
	for _, t := range targets {
		if tags, err := client.GetTags(tc, string(t.uid)); err == nil {
			fm.updateEntryTags(string(t.uid), tags)
		}
	}
	if fm.tagPanel != nil {
		fm.tagPanel.OnSelectionChanged()
	}
	if fm.other != nil && fm.other.isTie() {
		fm.other.reload()
	}

	if summary != nil {
		msg := fmt.Sprintf("%d of %d albums got MusicBrainz tags.", summary["added"], len(targets))
		if un, ok := summary["unmatched"].([]string); ok && len(un) > 0 {
			msg += "\n\nNo confident match:\n• " + strings.Join(un, "\n• ")
		}
		if fl, ok := summary["failed"].([]string); ok && len(fl) > 0 {
			msg += "\n\nFailed:\n• " + strings.Join(fl, "\n• ")
		}
		dialog.ShowInformation("MusicBrainz tags", msg, fm.win)
		return
	}
	if len(added) == 0 {
		name := musicBrainzNoticeText(rel)
		msg := "Those tags are already present."
		if rel == nil || len(rel.Genres)+len(rel.Tags) == 0 {
			msg = "That release has no genre/community tags to add."
		}
		dialog.ShowInformation("MusicBrainz tags", msg+"\n"+name, fm.win)
		return
	}
	dialog.ShowInformation("MusicBrainz tags",
		"Added tags:\n• "+strings.Join(added, "\n• ")+"\n\n"+musicBrainzNoticeText(rel), fm.win)
}

// addReleaseTagsOnly unions the release's genres and community tags into the
// album directory's tags, case-insensitively. It writes nothing else.
func addReleaseTagsOnly(tc *client.TieClient, uid client.DirUID, rel *musicbrainz.Release) ([]string, error) {
	if rel == nil {
		return nil, fmt.Errorf("no MusicBrainz release selected")
	}
	current, _ := client.GetTags(tc, string(uid))
	merged, added := musicBrainzTagUnion(current, rel.Genres, rel.Tags)
	if len(added) == 0 {
		return nil, nil
	}
	if err := client.SetTags(tc, string(uid), merged); err != nil {
		return nil, fmt.Errorf("set album tags: %w", err)
	}
	return added, nil
}

// applyReleaseTagsToAlbum resolves an album's tie: path and unions only the
// release's genre/community tags into it — the tags-only counterpart of
// applyReleaseToAlbum.
func applyReleaseTagsToAlbum(tc *client.TieClient, albumURI string, rel *musicbrainz.Release) error {
	uid, err := tieDirUID(tc, albumURI)
	if err != nil {
		return err
	}
	_, err = addReleaseTagsOnly(tc, uid, rel)
	return err
}

// musicBrainzTagUnion appends the release's genres and community tags to
// current, case-insensitively de-duplicated, preserving existing spellings.
func musicBrainzTagUnion(current, genres, community []string) (merged, added []string) {
	merged = append([]string(nil), current...)
	have := make(map[string]bool, len(current)+len(genres)+len(community))
	for _, tag := range current {
		have[strings.ToLower(tag)] = true
	}
	for _, tag := range append(append([]string(nil), genres...), community...) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if have[key] {
			continue
		}
		have[key] = true
		merged = append(merged, tag)
		added = append(added, tag)
	}
	return merged, added
}
