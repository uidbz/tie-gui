package musicbrainz

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestWebLive exercises the real MusicBrainz web service. Opt in because it
// needs internet and is rate limited:
//
//	TIE_FM_MB_WEB_TEST=1 go test ./cmd/tie-fm/internal/musicbrainz -run TestWebLive -v
func TestWebLive(t *testing.T) {
	if os.Getenv("TIE_FM_MB_WEB_TEST") == "" {
		t.Skip("set TIE_FM_MB_WEB_TEST=1 to query the public MusicBrainz API")
	}
	p, err := Open(Settings{Source: SourceWeb, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rels, err := p.SearchReleases(ctx, ReleaseQuery{Artist: "The Beatles", Album: "Abbey Road", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("no releases")
	}
	t.Logf("search: %s — %s (%d), %d tracks", rels[0].DisplayArtist(), rels[0].Title, rels[0].Year, rels[0].TrackCount)

	full, err := p.LookupRelease(ctx, rels[0].MBID)
	if err != nil {
		t.Fatal(err)
	}
	if !full.HasTracks() {
		t.Fatalf("lookup has no tracks: %+v", full)
	}
	t.Logf("lookup: %d tracks, cover=%v, genres=%v", full.TrackCount, full.HasCoverArt, full.Genres)

	recs, err := p.SearchRecordings(ctx, RecordingQuery{Artist: "The Beatles", Title: "Come Together", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("no recordings")
	}
	t.Logf("recording: %s — %s (%d releases)", recs[0].ArtistCredit, recs[0].Title, len(recs[0].Releases))
}
