package musicbrainz

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAndSimilarity(t *testing.T) {
	if got := Normalize("The Beatles & Co."); got != "the beatles and co" {
		t.Errorf("Normalize = %q", got)
	}
	cases := []struct {
		a, b string
		min  float64
	}{
		{"Abbey Road", "Abbey Road", 1},
		{"The Beatles", "Beatles", 0.9},
		{"Abbey Road (Remastered)", "Abbey Road", 0.7},
		{"Kind of Blue", "Kind of Blue", 1},
		{"Kind of Blue", "Bitches Brew", 0},
	}
	for _, c := range cases {
		if got := Similarity(c.a, c.b); got < c.min {
			t.Errorf("Similarity(%q,%q) = %.2f, want >= %.2f", c.a, c.b, got, c.min)
		}
	}
}

func TestScoreRelease(t *testing.T) {
	rel := Release{
		Title:        "Abbey Road",
		Artist:       Artist{Name: "The Beatles"},
		ArtistCredit: "The Beatles",
		Year:         1969,
		TrackCount:   17,
	}
	q := ReleaseQuery{Artist: "The Beatles", Album: "Abbey Road", Year: 1969, TrackCount: 17}
	if got := ScoreRelease(q, rel); got < 0.95 {
		t.Errorf("exact match score = %.2f", got)
	}
	wrongYear := q
	wrongYear.Year = 2005
	if got := ScoreRelease(wrongYear, rel); got >= 0.99 {
		t.Errorf("year mismatch should lower the score, got %.2f", got)
	}
	rels := []Release{
		{Title: "Something Else", Artist: Artist{Name: "The Beatles"}},
		{Title: "Abbey Road", Artist: Artist{Name: "The Beatles"}, Year: 1969},
	}
	ranked := RankReleases(q, rels)
	if len(ranked) != 2 || ranked[0].Title != "Abbey Road" {
		t.Fatalf("ranked = %+v", ranked)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Errorf("scores not sorted: %+v", ranked)
	}
}

func TestParseYear(t *testing.T) {
	if got := ParseYear("1969-09-26"); got != 1969 {
		t.Errorf("ParseYear = %d", got)
	}
	if got := ParseYear(""); got != 0 {
		t.Errorf("ParseYear(empty) = %d", got)
	}
}

const wsReleaseJSON = `{
  "releases": [{
    "id": "rel-1",
    "title": "Abbey Road",
    "date": "1969-09-26",
    "country": "GB",
    "status": "Official",
    "track-count": 17,
    "artist-credit": [{"name":"The Beatles","artist":{"id":"art-1","name":"The Beatles"}}],
    "release-group": {"id":"rg-1","title":"Abbey Road","primary-type":"Album","secondary-types":[]},
    "media": [{"position":1,"format":"CD","track-count":17}]
  }]
}`

const wsLookupJSON = `{
  "id": "rel-1",
  "title": "Abbey Road",
  "date": "1969-09-26",
  "country": "GB",
  "status": "Official",
  "artist-credit": [{"name":"The Beatles","artist":{"id":"art-1","name":"The Beatles"}}],
  "release-group": {"id":"rg-1","title":"Abbey Road","primary-type":"Album"},
  "genres": [{"name":"rock","count":5}],
  "cover-art-archive": {"front": true, "count": 1},
  "media": [{"position":1,"format":"CD","track-count":2,"tracks":[
    {"id":"t1","title":"Come Together","number":"1","position":1,"length":259000,
     "recording":{"id":"r1","title":"Come Together"},
     "artist-credit":[{"name":"The Beatles","artist":{"id":"art-1","name":"The Beatles"}}]},
    {"id":"t2","title":"Something","number":"2","position":2,"length":182000,
     "recording":{"id":"r2","title":"Something"},
     "artist-credit":[{"name":"The Beatles","artist":{"id":"art-1","name":"The Beatles"}}]}
  ]}]
}`

const wsRecordingJSON = `{
  "recordings": [{
    "id": "r1",
    "title": "Come Together",
    "length": 259000,
    "artist-credit": [{"name":"The Beatles","artist":{"id":"art-1","name":"The Beatles"}}],
    "releases": [{"id":"rel-1","title":"Abbey Road","date":"1969-09-26"}],
    "score": 100
  }]
}`

func newWebTestProvider(t *testing.T, handler http.Handler) *webProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := Open(Settings{Source: SourceWeb, WebBaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	wp := p.(*webProvider)
	wp.interval = 0
	wp.retryDelay = 0
	t.Cleanup(func() { wp.Close() })
	return wp
}

func TestWebSearchAndLookup(t *testing.T) {
	wp := newWebTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("missing User-Agent")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/ws/2/release/rel-1"):
			_, _ = w.Write([]byte(wsLookupJSON))
		case r.URL.Path == "/ws/2/release":
			_, _ = w.Write([]byte(wsReleaseJSON))
		case r.URL.Path == "/ws/2/recording":
			_, _ = w.Write([]byte(wsRecordingJSON))
		default:
			http.NotFound(w, r)
		}
	}))

	ctx := context.Background()
	rels, err := wp.SearchReleases(ctx, ReleaseQuery{Artist: "The Beatles", Album: "Abbey Road"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("releases = %d", len(rels))
	}
	if rels[0].Title != "Abbey Road" || rels[0].Year != 1969 || rels[0].TrackCount != 17 {
		t.Errorf("release = %+v", rels[0])
	}
	if rels[0].Artist.MBID != "art-1" {
		t.Errorf("artist MBID = %q", rels[0].Artist.MBID)
	}

	full, err := wp.LookupRelease(ctx, "rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if !full.HasCoverArt || len(full.Genres) != 1 || full.Genres[0] != "rock" {
		t.Errorf("lookup = %+v", full)
	}
	if len(full.Mediums) != 1 || len(full.Mediums[0].Tracks) != 2 {
		t.Fatalf("mediums = %+v", full.Mediums)
	}
	if full.Mediums[0].Tracks[0].RecordingMBID != "r1" {
		t.Errorf("track = %+v", full.Mediums[0].Tracks[0])
	}
	if got := full.CoverArtURL("https://caa.example"); got != "https://caa.example/release/rel-1/front-500" {
		t.Errorf("CoverArtURL = %q", got)
	}

	recs, err := wp.SearchRecordings(ctx, RecordingQuery{Artist: "The Beatles", Title: "Come Together"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].MBID != "r1" || len(recs[0].Releases) != 1 {
		t.Fatalf("recordings = %+v", recs)
	}
}

func TestWebErrorStatus(t *testing.T) {
	wp := newWebTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusServiceUnavailable)
	}))
	_, err := wp.SearchReleases(context.Background(), ReleaseQuery{Album: "x"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v", err)
	}
}

func TestWebRateLimit(t *testing.T) {
	wp := newWebTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"releases":[]}`))
	}))
	wp.interval = 80 * time.Millisecond
	start := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := wp.SearchReleases(context.Background(), ReleaseQuery{Album: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Errorf("requests were not rate limited: %v", elapsed)
	}
}

// Ensure the JSON fixtures stay valid.
func TestWebFixturesValid(t *testing.T) {
	for _, s := range []string{wsReleaseJSON, wsLookupJSON, wsRecordingJSON} {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			t.Fatalf("invalid fixture: %v", err)
		}
	}
}
