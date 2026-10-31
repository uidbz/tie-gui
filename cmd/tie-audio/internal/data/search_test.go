package data

import (
	"testing"

	"github.com/uidbz/tie/client"
)

func row(key string, attrs map[string][]string) client.Row {
	return client.Row{Key: key, Attributes: attrs}
}

func TestAlbumsFromRowsGroupsTracksIntoAlbums(t *testing.T) {
	rows := []client.Row{
		row("album-1", map[string][]string{
			"tie-type": {"audio-dir"},
			"parent":   {"root"},
		}),
		row("track-1", map[string][]string{
			"tie-type":  {"audio-file"},
			"title":     {"Come Together"},
			"album":     {"Abbey Road"},
			"artist":    {"The Beatles"},
			"year":      {"1969"},
			"parent":    {"album-1"},
			"member-of": {"album-1"},
		}),
		row("standalone", map[string][]string{
			"tie-type": {"audio-file"},
			"title":    {"Loose Track"},
			"artist":   {"Nobody"},
		}),
	}
	albums := (&Session{}).albumsFromRows(rows)
	if len(albums) != 2 {
		t.Fatalf("albums = %+v, want the album plus the standalone track", albums)
	}
	var dir, track *Album
	for i := range albums {
		switch albums[i].UID {
		case "album-1":
			dir = &albums[i]
		case "standalone":
			track = &albums[i]
		}
	}
	if dir == nil {
		t.Fatal("album dir missing")
	}
	if dir.Kind != AlbumDir || dir.Title != "Abbey Road" || dir.Artist != "The Beatles" || dir.Year != "1969" {
		t.Errorf("album dir = %+v, want metadata inherited from its track", dir)
	}
	if track == nil || track.Kind != AlbumTrack || track.Title != "Loose Track" {
		t.Errorf("standalone track = %+v", track)
	}
}

func TestSearchWithinFiltersToSelection(t *testing.T) {
	result := SearchResult{
		Albums: []Album{{UID: "a"}, {UID: "b"}, {UID: "c"}},
		Total:  12,
	}
	got := SearchWithin(result, map[string]bool{"a": true, "c": true})
	if len(got.Albums) != 2 || got.Albums[0].UID != "a" || got.Albums[1].UID != "c" {
		t.Errorf("filtered albums = %+v", got.Albums)
	}
	if got.Total != 2 {
		t.Errorf("filtered total = %d, want 2", got.Total)
	}
	if empty := SearchWithin(result, nil); len(empty.Albums) != 0 || empty.Total != 0 {
		t.Errorf("empty selection = %+v", empty)
	}

	// A track hit from a selected album survives the restriction.
	withTrack := SearchResult{Albums: []Album{
		{UID: "album-1", Kind: AlbumDir},
		{UID: "track-1", Kind: AlbumTrack, OwnerUID: "album-1"},
		{UID: "track-2", Kind: AlbumTrack, OwnerUID: "elsewhere"},
	}}
	got = SearchWithin(withTrack, map[string]bool{"album-1": true})
	if len(got.Albums) != 2 || got.Albums[1].UID != "track-1" {
		t.Errorf("selection kept %+v, want the album and its track hit", got.Albums)
	}
}

func TestSearchAlbumsFromHitsShowsMatchedTracks(t *testing.T) {
	matches := []client.SearchMatch{
		{Key: "album-1", Relation: client.RelAlbum, Value: "Abbey Road", Attributes: map[string][]string{
			"tie-type": {"audio-dir"},
			"album":    {"Abbey Road"},
		}},
		// Folds into album-1: the album itself matched too.
		{Key: "track-1", Relation: client.RelTitle, Value: "Come Together", Attributes: map[string][]string{
			"tie-type":  {"audio-file"},
			"title":     {"Come Together"},
			"album":     {"Abbey Road"},
			"artist":    {"The Beatles"},
			"member-of": {"album-1"},
			"parent":    {"album-1"},
		}},
		// Its album did not match: the hit stays a track tile.
		{Key: "track-2", Relation: client.RelTitle, Value: "Love", Attributes: map[string][]string{
			"tie-type":  {"audio-file"},
			"title":     {"Love"},
			"album":     {"Manga Techno"},
			"artist":    {"Dr.Tikov"},
			"member-of": {"album-2"},
			"parent":    {"album-2"},
		}},
		// Standalone track: no owner.
		{Key: "loose", Relation: client.RelTitle, Value: "Loose", Attributes: map[string][]string{
			"tie-type": {"audio-file"},
			"title":    {"Loose"},
			"artist":   {"Nobody"},
		}},
	}
	owners := []client.Row{row("album-2", map[string][]string{
		"tie-type":  {"audio-dir"},
		"album":     {"Manga Techno"},
		"thumbnail": {"thumb-2"},
	})}

	albums := searchAlbumsFromHits(matches, owners)
	if len(albums) != 3 {
		t.Fatalf("albums = %+v, want album, track hit and standalone in rank order", albums)
	}
	if albums[0].UID != "album-1" || albums[0].Kind != AlbumDir {
		t.Errorf("first entry = %+v, want the matched album", albums[0])
	}
	tr := albums[1]
	if tr.UID != "track-2" || tr.Kind != AlbumTrack || tr.Title != "Love" {
		t.Errorf("track tile = %+v, want the matched song title", tr)
	}
	if tr.AlbumTitle != "Manga Techno" || tr.OwnerUID != "album-2" || tr.CoverUID != "album-2" {
		t.Errorf("track tile owner = %+v, want album-2 for art/open", tr)
	}
	if tr.OwnerKind != AlbumDir || tr.ThumbHash != "thumb-2" {
		t.Errorf("track tile owner kind/thumb = %v/%q, want dir/thumb-2", tr.OwnerKind, tr.ThumbHash)
	}
	if tr.Artist != "Dr.Tikov" {
		t.Errorf("track tile artist = %q, want the hit's artist", tr.Artist)
	}
	if loose := albums[2]; loose.UID != "loose" || loose.OwnerUID != "" || loose.CoverUID != "" {
		t.Errorf("standalone tile = %+v, want no owner", loose)
	}
}

func TestSearchSpecAppliesOptions(t *testing.T) {
	// Zero options = the audio profile's defaults, expanded and page-sized.
	base := searchSpec("beatles", 0, SearchOptions{})
	profile := client.AudioSearchProfile.Spec("beatles")
	if base.Mode != profile.Mode || len(base.Relations) != len(profile.Relations) {
		t.Errorf("default spec = %+v, want the profile's mode/vocabulary", base)
	}
	if !base.Expand || base.Limit != searchPageSize {
		t.Errorf("default spec expand/limit = %v/%d, want true/%d", base.Expand, base.Limit, searchPageSize)
	}

	got := searchSpec("blu", 25, SearchOptions{
		Mode:   client.SearchFuzzy,
		Fields: []client.Relation{client.RelTitle, client.RelTag},
		Scope:  SearchTracksOnly,
	})
	if got.Mode != client.SearchFuzzy {
		t.Errorf("mode = %q, want fuzzy", got.Mode)
	}
	if len(got.Relations) != 2 || got.Relations[0] != client.RelTitle {
		t.Errorf("relations = %v, want the field restriction", got.Relations)
	}
	if len(got.TieTypes) != 1 || got.TieTypes[0] != client.TieAudioFile {
		t.Errorf("tie types = %v, want audio-file only", got.TieTypes)
	}
	if got.Limit != 25 {
		t.Errorf("limit = %d, want 25", got.Limit)
	}

	got = searchSpec("x", 0, SearchOptions{Scope: SearchAlbumsOnly})
	if len(got.TieTypes) != 2 || got.TieTypes[0] != client.TieAudioDir || got.TieTypes[1] != client.TieAudioArchive {
		t.Errorf("albums-only tie types = %v, want dir+archive", got.TieTypes)
	}

	got = searchSpec("x", 0, SearchOptions{})
	if len(got.TieTypes) != len(profile.TieTypes) {
		t.Errorf("everything-scope tie types = %v, want the profile's", got.TieTypes)
	}
}

func TestTrackFromMatchResolvesAlbumAndNumbers(t *testing.T) {
	m := client.SearchMatch{
		Key:   "hash-1",
		Score: 9,
		Attributes: map[string][]string{
			"title":     {"Come Together"},
			"artist":    {"The Beatles"},
			"album":     {"Abbey Road"},
			"year":      {"1969"},
			"track":     {"1"},
			"disc":      {"2"},
			"duration":  {"261.5"},
			"member-of": {"album-1"},
			"parent":    {"album-1"},
		},
	}
	tr := trackFromMatch(m)
	if tr.Hash != "hash-1" || tr.Title != "Come Together" || tr.Artist != "The Beatles" || tr.Album != "Abbey Road" {
		t.Errorf("track = %+v, want the expanded attributes", tr)
	}
	if tr.TrackNo != 1 || tr.Disc != 2 || tr.Duration != 261.5 {
		t.Errorf("numbers = %d/%d/%v, want 1/2/261.5", tr.TrackNo, tr.Disc, tr.Duration)
	}
	if tr.AlbumUID != "album-1" {
		t.Errorf("album uid = %q, want member-of first", tr.AlbumUID)
	}

	// Filename-only row: title falls back, album resolves through parent, and
	// a track with no relations at all keys off its own hash.
	tr = trackFromMatch(client.SearchMatch{Key: "h2", Attributes: map[string][]string{
		"filename": {"loose.mp3"},
		"parent":   {"dir-9"},
	}})
	if tr.Title != "loose.mp3" || tr.AlbumUID != "dir-9" {
		t.Errorf("fallback track = %+v", tr)
	}
	tr = trackFromMatch(client.SearchMatch{Key: "h3"})
	if tr.AlbumUID != "h3" {
		t.Errorf("standalone album uid = %q, want own hash", tr.AlbumUID)
	}
}
