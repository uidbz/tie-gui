package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

// MusicBrainz relation names written by the tagging actions. They are not part
// of tie's media vocabulary, so they stay plain strings (the same escape hatch
// tie-fm uses for album-artist/duration).
const (
	relMBReleaseID      = "musicbrainz-release-id"
	relMBReleaseGroupID = "musicbrainz-release-group-id"
	relMBArtistID       = "musicbrainz-artist-id"
	relMBRecordingID    = "musicbrainz-recording-id"
	relMBTrackID        = "musicbrainz-track-id"
)

// musicBrainzSettings maps tie-fm's persisted settings to the provider's.
func musicBrainzSettings(c config.MusicBrainz) musicbrainz.Settings {
	return musicbrainz.Settings{
		Source:          c.Source,
		DSN:             c.DBDSN,
		WebBaseURL:      c.WebBaseURL,
		CoverArtBaseURL: c.CoverArtBaseURL,
		Contact:         c.Contact,
	}
}

// openMusicBrainz constructs the configured backend.
func openMusicBrainz(c config.MusicBrainz) (musicbrainz.Provider, error) {
	return musicbrainz.Open(musicBrainzSettings(c))
}

// tieDirUID resolves a tie: URI to a directory UID.
func tieDirUID(tc *client.TieClient, uri string) (client.DirUID, error) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(uri, client.FileURIScheme) {
		uri = client.FileURIScheme + uri
	}
	uid, err := tc.DirUIDFromPath(uri)
	if err != nil {
		return "", err
	}
	if uid == "" {
		return "", fmt.Errorf("album directory is not in the tie store: %s", uri)
	}
	return uid, nil
}

// tieFilesUnder returns every file below an album directory (recursively,
// including cd1/cd2 disc directories).
func tieFilesUnder(tc *client.TieClient, uid client.DirUID) ([]client.File, error) {
	var out []client.File
	var walk func(client.DirUID, int) error
	walk = func(dir client.DirUID, depth int) error {
		if depth > 6 {
			return nil
		}
		d, err := client.ReadTieDir(tc, dir)
		if err != nil {
			return err
		}
		out = append(out, d.Files...)
		for _, sub := range d.SubDirs {
			if err := walk(sub.Uid, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(uid, 0); err != nil {
		return nil, err
	}
	return out, nil
}

// albumHasLocalArt reports whether the album directory already carries a
// thumbnail relation or contains an image file.
func albumHasLocalArt(tc *client.TieClient, uid client.DirUID) bool {
	if row, err := tc.Get(string(uid)); err == nil {
		if len(client.RowValues(row, client.RelThumbnail)) > 0 {
			return true
		}
	}
	files, err := tieFilesUnder(tc, uid)
	if err != nil {
		return false
	}
	for _, f := range files {
		if f.TieType == client.TieImageFile {
			return true
		}
	}
	return false
}

// applyReleaseToAlbum writes a MusicBrainz release onto an imported/existing
// album directory: album artist/album/year, MBIDs, genre tags, per-track
// title/artist/track/disc numbers and recording IDs, and optionally a front
// cover when the album has none.
func applyReleaseToAlbum(tc *client.TieClient, albumURI string, rel *musicbrainz.Release, fetchCover bool, settings config.MusicBrainz) error {
	uid, err := tieDirUID(tc, albumURI)
	if err != nil {
		return err
	}
	dirKey := string(uid)

	batch := tc.NewBatch()
	artist := rel.DisplayArtist()
	if artist != "" {
		batch.Set(dirKey, "artist", []string{artist})
		batch.Set(dirKey, "album-artist", []string{artist})
	}
	if rel.Title != "" {
		batch.Set(dirKey, "album", []string{rel.Title})
	}
	if rel.Year > 0 {
		batch.Set(dirKey, "year", []string{strconv.Itoa(rel.Year)})
	}
	if rel.MBID != "" {
		batch.Set(dirKey, relMBReleaseID, []string{rel.MBID})
	}
	if rel.ReleaseGroupMBID != "" {
		batch.Set(dirKey, relMBReleaseGroupID, []string{rel.ReleaseGroupMBID})
	}
	if rel.Artist.MBID != "" {
		batch.Set(dirKey, relMBArtistID, []string{rel.Artist.MBID})
	}

	files, err := tieFilesUnder(tc, uid)
	if err != nil {
		return err
	}
	hashes := make([]string, 0, len(files))
	for _, f := range files {
		if f.Uid != "" {
			hashes = append(hashes, f.Uid)
		}
	}
	rows, _ := tc.Expand(hashes)
	rowByHash := make(map[string]client.Row, len(rows))
	for _, r := range rows {
		rowByHash[r.Key] = r
	}
	albumFiles := make([]albumFile, 0, len(files))
	for _, f := range files {
		if f.TieType != client.TieAudioFile {
			continue // never stamp track metadata onto covers/logs
		}
		af := albumFile{hash: f.Uid, name: f.Filename}
		if row, ok := rowByHash[f.Uid]; ok {
			af.disc = parseRowInt(row, "disc")
			af.track = parseRowInt(row, "track")
			af.title = client.RowFirst(row, "title")
		}
		albumFiles = append(albumFiles, af)
	}

	matched := matchAlbumTracks(albumFiles, rel)
	for _, m := range matched {
		hash := m.file.hash
		t := m.track
		batch.Set(hash, "title", []string{t.Title})
		if t.ArtistCredit != "" {
			batch.Set(hash, "artist", []string{t.ArtistCredit})
		} else if t.Artist != "" {
			batch.Set(hash, "artist", []string{t.Artist})
		}
		if album := rel.Title; album != "" {
			batch.Set(hash, "album", []string{album})
		}
		if artist != "" {
			batch.Set(hash, "album-artist", []string{artist})
		}
		if rel.Year > 0 {
			batch.Set(hash, "year", []string{strconv.Itoa(rel.Year)})
		}
		if t.Position > 0 {
			batch.Set(hash, "track", []string{strconv.Itoa(t.Position)})
		}
		if t.Disc > 0 {
			batch.Set(hash, "disc", []string{strconv.Itoa(t.Disc)})
		}
		if medium := mediumForTrack(rel, t); medium != nil && medium.TrackCount > 0 {
			batch.Set(hash, "track-total", []string{strconv.Itoa(medium.TrackCount)})
		}
		if rel.DiscCount > 0 {
			batch.Set(hash, "disc-total", []string{strconv.Itoa(rel.DiscCount)})
		}
		if t.RecordingMBID != "" {
			batch.Set(hash, relMBRecordingID, []string{t.RecordingMBID})
		}
		if t.MBID != "" {
			batch.Set(hash, relMBTrackID, []string{t.MBID})
		}
		if rel.MBID != "" {
			batch.Set(hash, relMBReleaseID, []string{rel.MBID})
		}
		if rel.ReleaseGroupMBID != "" {
			batch.Set(hash, relMBReleaseGroupID, []string{rel.ReleaseGroupMBID})
		}
		if rel.Artist.MBID != "" {
			batch.Set(hash, relMBArtistID, []string{rel.Artist.MBID})
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("MusicBrainz release has no tracks to apply")
	}
	if _, err := tc.Batch(batch); err != nil {
		return err
	}
	if err := tc.Sync(); err != nil {
		return err
	}

	// Genres become ordinary tags (union with existing), so they are
	// browsable in the tag panel and queryable in tie-audio.
	if len(rel.Genres) > 0 {
		tags, _ := client.GetTags(tc, dirKey)
		seen := make(map[string]bool, len(tags))
		for _, t := range tags {
			seen[t] = true
		}
		for _, g := range rel.Genres {
			if !seen[g] {
				tags = append(tags, g)
				seen[g] = true
			}
		}
		if err := client.SetTags(tc, dirKey, tags); err != nil {
			return fmt.Errorf("albums tags: %w", err)
		}
	}

	// Refresh the collection contract (member-of edges, album summary) when the
	// directory is a media collection; a plain directory simply has nothing to
	// index, which is not a tagging failure.
	_, _ = tc.IndexCollection(uid, client.IndexOptions{})

	if fetchCover {
		// Cover art is optional: a Cover Art Archive outage must not turn a
		// successful tag write into a reported failure.
		_ = ensureAlbumCoverArt(tc, uid, rel, settings)
	}
	return nil
}

// applyRecordingToFile writes a single-track MusicBrainz match onto a file.
func applyRecordingToFile(tc *client.TieClient, hash string, rec *musicbrainz.Recording) error {
	if hash == "" {
		return fmt.Errorf("tie entry has no content hash")
	}
	batch := tc.NewBatch()
	if rec.Title != "" {
		batch.Set(hash, "title", []string{rec.Title})
	}
	artist := rec.ArtistCredit
	if artist == "" {
		artist = rec.Artist
	}
	if artist != "" {
		batch.Set(hash, "artist", []string{artist})
	}
	if rec.MBID != "" {
		batch.Set(hash, relMBRecordingID, []string{rec.MBID})
	}
	if rec.ArtistMBID != "" {
		batch.Set(hash, relMBArtistID, []string{rec.ArtistMBID})
	}
	if len(rec.Releases) > 0 {
		batch.Set(hash, relMBReleaseID, []string{rec.Releases[0].MBID})
		if rec.Releases[0].Title != "" {
			batch.Set(hash, "album", []string{rec.Releases[0].Title})
		}
		if rec.Releases[0].Year > 0 {
			batch.Set(hash, "year", []string{strconv.Itoa(rec.Releases[0].Year)})
		}
	}
	if _, err := tc.Batch(batch); err != nil {
		return err
	}
	return tc.Sync()
}

// ensureAlbumCoverArt downloads the release's front cover and records it as the
// album's thumbnail, unless the directory already has art.
func ensureAlbumCoverArt(tc *client.TieClient, uid client.DirUID, rel *musicbrainz.Release, settings config.MusicBrainz) error {
	if !rel.HasCoverArt {
		return nil
	}
	if albumHasLocalArt(tc, uid) {
		return nil
	}
	url := rel.CoverArtURL(settings.CoverArtBaseURL)
	if url == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	data, err := musicbrainz.FetchCoverArt(ctx, url, musicBrainzSettings(settings))
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "tie-fm-mb-cover-*"+musicbrainz.CoverArtExtension(data))
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	up, err := tc.UploadWithOptions("", tmpName, client.UploadOptions{Retention: "infinite"})
	if err != nil {
		return fmt.Errorf("upload cover art: %w", err)
	}
	if up == nil || len(up.Items) == 0 || up.Items[0].Hash == "" {
		return fmt.Errorf("upload cover art: empty result")
	}
	batch := tc.NewBatch()
	batch.Add(string(uid), string(client.RelThumbnail), up.Items[0].Hash)
	if _, err := tc.Batch(batch); err != nil {
		return err
	}
	return tc.Sync()
}

type albumFile struct {
	hash  string
	name  string
	title string
	disc  int
	track int
}

type matchedTrack struct {
	file  albumFile
	track musicbrainz.Track
}

// matchAlbumTracks pairs MusicBrainz track positions with the files of an
// album directory. Matching is by (disc, track), then track alone (single-disc
// or missing disc tags), then title, then remaining-order.
func matchAlbumTracks(files []albumFile, rel *musicbrainz.Release) []matchedTrack {
	used := make([]bool, len(files))
	byPos := map[string]int{}
	byTitle := map[string][]int{}
	for i, f := range files {
		if f.track > 0 {
			byPos[fmt.Sprintf("%d:%d", f.disc, f.track)] = i
		}
		t := strings.ToLower(strings.TrimSpace(f.title))
		if t == "" {
			t = strings.ToLower(strings.TrimSpace(f.name))
		}
		if t != "" {
			byTitle[t] = append(byTitle[t], i)
		}
	}
	var out []matchedTrack
	for _, t := range rel.FlatTracks() {
		idx := -1
		if i, ok := byPos[fmt.Sprintf("%d:%d", t.Disc, t.Position)]; ok && !used[i] {
			idx = i
		}
		if idx < 0 && rel.DiscCount <= 1 {
			if i, ok := byPos[fmt.Sprintf("0:%d", t.Position)]; ok && !used[i] {
				idx = i
			}
		}
		if idx < 0 {
			key := strings.ToLower(strings.TrimSpace(t.Title))
			for _, i := range byTitle[key] {
				if !used[i] {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			// Last resort: the next unmatched file, in release order.
			for i := range files {
				if !used[i] {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			break
		}
		used[idx] = true
		out = append(out, matchedTrack{file: files[idx], track: t})
	}
	return out
}

func mediumForTrack(rel *musicbrainz.Release, t musicbrainz.Track) *musicbrainz.Medium {
	for i := range rel.Mediums {
		if rel.Mediums[i].Position == t.Disc {
			return &rel.Mediums[i]
		}
	}
	return nil
}

func parseRowInt(row client.Row, relation string) int {
	v := client.RowFirst(row, client.Relation(relation))
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

// sanitizePathSegment makes a metadata value safe to embed in one virtual path
// segment (the same rules as tie's import destination renderer).
func sanitizePathSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\':
			return ' '
		case r < 0x20:
			return -1
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	if s == "." || s == ".." {
		return ""
	}
	return s
}

// musicBrainzNoticeText summarises a release for labels.
func musicBrainzNoticeText(rel *musicbrainz.Release) string {
	if rel == nil {
		return ""
	}
	parts := []string{}
	if rel.DisplayArtist() != "" {
		parts = append(parts, rel.DisplayArtist())
	}
	if rel.Title != "" {
		parts = append(parts, rel.Title)
	}
	if rel.YearString() != "" {
		parts = append(parts, "("+rel.YearString()+")")
	}
	return strings.Join(parts, " — ")
}
