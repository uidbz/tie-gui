package ui

import (
	"testing"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// A search-result track tile names the song as its title and the owning album
// on the subtitle, so a hit on the album (or artist) field still explains
// itself; an album tile keeps showing just its artist.
func TestAudioAlbumItemTrackSubtitle(t *testing.T) {
	tile := &AudioAlbumItem{album: data.Album{
		UID:        "track-1",
		Kind:       data.AlbumTrack,
		Title:      "Love",
		Artist:     "Dr.Tikov",
		AlbumTitle: "Manga Techno",
	}}
	if got := tile.DisplayName(); got != "Love" {
		t.Errorf("track tile name = %q, want the song title", got)
	}
	if got := tile.Subtitle(); got != "Dr.Tikov · Manga Techno" {
		t.Errorf("track tile subtitle = %q, want artist and album", got)
	}

	album := &AudioAlbumItem{album: data.Album{
		UID:    "album-1",
		Kind:   data.AlbumDir,
		Title:  "Manga Techno",
		Artist: "Dr.Tikov",
	}}
	if got := album.Subtitle(); got != "Dr.Tikov" {
		t.Errorf("album tile subtitle = %q, want the artist only", got)
	}
}
