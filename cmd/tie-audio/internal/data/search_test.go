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
}
