package musicbrainz

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLocalProviderIntegration runs against the full local MusicBrainz
// database. It is opt-in because it needs the imported mirror:
//
//	TIE_FM_MB_TEST=1 TIE_FM_MB_DSN=postgres://... go test ./cmd/tie-fm/internal/musicbrainz
func TestLocalProviderIntegration(t *testing.T) {
	if os.Getenv("TIE_FM_MB_TEST") == "" {
		t.Skip("set TIE_FM_MB_TEST=1 to test against the local MusicBrainz database")
	}
	settings := Settings{Source: SourceLocal}
	if dsn := os.Getenv("TIE_FM_MB_DSN"); dsn != "" {
		settings.DSN = dsn
	}
	p, err := Open(settings)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	rels, err := p.SearchReleases(ctx, ReleaseQuery{Artist: "The Beatles", Album: "Abbey Road", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("no releases found for The Beatles / Abbey Road")
	}
	t.Logf("search: %s — %s (%d)", rels[0].DisplayArtist(), rels[0].Title, rels[0].Year)

	full, err := p.LookupRelease(ctx, rels[0].MBID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Mediums) == 0 || len(full.Mediums[0].Tracks) == 0 {
		t.Fatalf("lookup has no tracks: %+v", full)
	}
	t.Logf("lookup: %d tracks, first=%q artist=%q", full.TrackCount,
		full.Mediums[0].Tracks[0].Title, full.Mediums[0].Tracks[0].ArtistCredit)

	recs, err := p.SearchRecordings(ctx, RecordingQuery{Artist: "The Beatles", Title: "Come Together", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("no recordings found for The Beatles / Come Together")
	}
	t.Logf("recording: %s — %s", recs[0].ArtistCredit, recs[0].Title)
}
