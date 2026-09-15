package data

import "testing"

// A track reachable from several directories — its album plus any playlist that
// lists it — must be attributed to the album, not the playlist, or the queue
// would group a saved playlist's tracks under the playlist itself and show one
// cover for all of them.
func TestTrackAlbumUID(t *testing.T) {
	tests := []struct {
		name    string
		parents []string
		exclude string
		want    string
	}{
		{"single parent", []string{"album-a"}, "", "album-a"},
		{"no parents", nil, "", ""},
		{"playlist excluded", []string{"playlist-1", "album-a"}, "playlist-1", "album-a"},
		{"playlist first in list", []string{"album-a", "playlist-1"}, "playlist-1", "album-a"},
		{"only the excluded parent", []string{"playlist-1"}, "playlist-1", ""},
		{"empty values skipped", []string{"", "album-a"}, "", "album-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trackAlbumUID(tt.parents, tt.exclude); got != tt.want {
				t.Errorf("trackAlbumUID(%v, %q) = %q, want %q", tt.parents, tt.exclude, got, tt.want)
			}
		})
	}
}
