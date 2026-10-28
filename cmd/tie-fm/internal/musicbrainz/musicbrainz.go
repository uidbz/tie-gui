// Package musicbrainz provides MusicBrainz metadata lookups for tie-fm.
//
// Two interchangeable backends are available:
//
//   - "local": the full MusicBrainz PostgreSQL database (schema "musicbrainz"
//     plus "cover_art_archive") imported by the companion local-mirror setup.
//     Offline, no rate limit, exact/prefix matching plus Go-side fuzzy scoring.
//   - "web": the public MusicBrainz web service (https://musicbrainz.org/ws/2).
//     Lucene search, needs internet, ~1 request/second.
//
// Both return the same Release/Recording models so the UI does not care which
// backend answered.
package musicbrainz

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Backend identifiers used by Settings.Source and the settings UI.
const (
	SourceLocal = "local"
	SourceWeb   = "web"
)

// Common defaults for the local mirror created alongside tie-fm.
const (
	DefaultDSN             = "postgres://musicbrainz:musicbrainz@127.0.0.1:5432/musicbrainz_db?sslmode=disable"
	DefaultWebBaseURL      = "https://musicbrainz.org"
	DefaultCoverArtBaseURL = "https://coverartarchive.org"
	DefaultUserAgent       = "tie-fm/1.0 ( https://github.com/uidbz/tie-gui )"
	DefaultTimeout         = 20 * time.Second
)

// ErrNotConfigured is returned when no usable backend is selected.
var ErrNotConfigured = errors.New("musicbrainz: no backend configured")

// Settings selects and configures a backend.
type Settings struct {
	Source          string        // SourceLocal or SourceWeb
	DSN             string        // local: postgres://...
	WebBaseURL      string        // web: https://musicbrainz.org
	CoverArtBaseURL string        // cover art archive root
	Contact         string        // optional contact appended to the User-Agent
	UserAgent       string        // complete override
	Timeout         time.Duration // per-request timeout (0 = DefaultTimeout)
}

// WithDefaults fills empty fields with the local-mirror defaults.
func (s Settings) WithDefaults() Settings {
	if s.Source == "" {
		s.Source = SourceLocal
	}
	if s.DSN == "" {
		s.DSN = DefaultDSN
	}
	if s.WebBaseURL == "" {
		s.WebBaseURL = DefaultWebBaseURL
	}
	if s.CoverArtBaseURL == "" {
		s.CoverArtBaseURL = DefaultCoverArtBaseURL
	}
	s.WebBaseURL = strings.TrimRight(s.WebBaseURL, "/")
	s.CoverArtBaseURL = strings.TrimRight(s.CoverArtBaseURL, "/")
	s.UserAgent = strings.TrimSpace(s.UserAgent)
	if s.UserAgent == "" {
		s.UserAgent = DefaultUserAgent
		if c := strings.TrimSpace(s.Contact); c != "" {
			s.UserAgent = fmt.Sprintf("tie-fm/1.0 ( %s )", c)
		}
	}
	if s.Timeout <= 0 {
		s.Timeout = DefaultTimeout
	}
	return s
}

// Provider is one MusicBrainz backend.
type Provider interface {
	// Kind reports SourceLocal or SourceWeb.
	Kind() string
	// Ping verifies that the backend is reachable.
	Ping(ctx context.Context) error
	// SearchReleases returns candidate releases, best match first.
	SearchReleases(ctx context.Context, q ReleaseQuery) ([]Release, error)
	// LookupRelease fetches one release with its mediums and tracks.
	LookupRelease(ctx context.Context, mbid string) (*Release, error)
	// SearchRecordings returns candidate recordings, best match first.
	SearchRecordings(ctx context.Context, q RecordingQuery) ([]Recording, error)
	Close() error
}

// Open constructs a backend. It does not connect until the first request
// (local) or Ping (web).
func Open(s Settings) (Provider, error) {
	s = s.WithDefaults()
	switch s.Source {
	case SourceLocal:
		return openLocal(s)
	case SourceWeb:
		return openWeb(s)
	case "", "off", "none", "disabled":
		return nil, ErrNotConfigured
	default:
		return nil, fmt.Errorf("musicbrainz: unknown source %q", s.Source)
	}
}

// ReleaseQuery describes an album lookup (either field may be empty).
type ReleaseQuery struct {
	Artist     string
	Album      string
	Year       int
	TrackCount int
	Limit      int
}

// RecordingQuery describes a single-track lookup.
type RecordingQuery struct {
	Artist string
	Title  string
	Limit  int
}

// Artist is a MusicBrainz artist identity.
type Artist struct {
	MBID string
	Name string
}

// Release is the subset of a MusicBrainz release tie-fm needs.
type Release struct {
	MBID              string
	Title             string
	Artist            Artist
	ArtistCredit      string
	ReleaseGroupMBID  string
	ReleaseGroupTitle string
	Date              string
	Year              int
	Country           string
	Status            string
	PrimaryType       string
	SecondaryTypes    []string
	Genres            []string
	Tags              []string
	TrackCount        int
	DiscCount         int
	Mediums           []Medium
	HasCoverArt       bool
	// CoverArtMBID overrides MBID for the Cover Art Archive URL when the
	// release has no art of its own but its release group does.
	CoverArtMBID string
	Score        float64
}

// Medium is one disc of a release.
type Medium struct {
	Position   int
	Format     string
	TrackCount int
	Tracks     []Track
}

// Track is one track of a release.
type Track struct {
	MBID          string
	RecordingMBID string
	Title         string
	Artist        string
	ArtistCredit  string
	Position      int
	Number        string
	Disc          int
	LengthMS      int
}

// Recording is a single-track lookup result.
type Recording struct {
	MBID         string
	Title        string
	Artist       string
	ArtistMBID   string
	ArtistCredit string
	LengthMS     int
	Releases     []RecordingRelease
	Score        float64
}

// RecordingRelease is a release containing a recording.
type RecordingRelease struct {
	MBID  string
	Title string
	Year  int
}

// HasTracks reports whether the release carries a track listing (search
// results may include mediums with counts but no tracks; callers then need a
// LookupRelease before writing per-track metadata).
func (r Release) HasTracks() bool {
	for _, m := range r.Mediums {
		if len(m.Tracks) > 0 {
			return true
		}
	}
	return false
}

// FlatTracks returns every track with its disc number filled in.
func (r Release) FlatTracks() []Track {
	var out []Track
	for _, m := range r.Mediums {
		for _, t := range m.Tracks {
			t.Disc = m.Position
			out = append(out, t)
		}
	}
	return out
}

// YearString renders the release year, or "" when unknown.
func (r Release) YearString() string {
	if r.Year == 0 {
		return ""
	}
	return strconv.Itoa(r.Year)
}

// DisplayArtist returns the artist credit, falling back to the artist name.
func (r Release) DisplayArtist() string {
	if r.ArtistCredit != "" {
		return r.ArtistCredit
	}
	return r.Artist.Name
}

// CoverArtURL builds the Cover Art Archive front image URL for the release.
func (r Release) CoverArtURL(base string) string {
	mbid := r.CoverArtMBID
	if mbid == "" {
		mbid = r.MBID
	}
	if mbid == "" {
		return ""
	}
	if base == "" {
		base = DefaultCoverArtBaseURL
	}
	return strings.TrimRight(base, "/") + "/release/" + url.PathEscape(mbid) + "/front-500"
}

// ParseYear extracts a four-digit year from an ISO date.
func ParseYear(date string) int {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil && y > 0 {
			return y
		}
	}
	return 0
}

// Normalize lowercases a string and strips punctuation/diacritics-lite so
// "The Beatles" and "beatles" compare closely. It deliberately stays small:
// enough for matching tags against MusicBrainz names, not a full collator.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '&':
			// "&" is a word: pad it on BOTH sides, otherwise "Simon&Sophie"
			// normalizes to "simonandsophie" and never matches the
			// "simon and sophie" the rewrite exists to create.
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("and")
			space = true
		case r > 127:
			// Keep non-ASCII letters (Cyrillic, Greek, CJK...) as-is.
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// stripLeadingThe removes an English leading article for comparison only.
func stripLeadingThe(s string) string {
	if strings.HasPrefix(s, "the ") {
		return s[4:]
	}
	return s
}

// Similarity scores two strings in [0,1]. It combines exact/prefix/contains
// matches with token overlap, so "Abbey Road (Remastered)" still matches
// "Abbey Road".
func Similarity(a, b string) float64 {
	na, nb := Normalize(a), Normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	if stripLeadingThe(na) == stripLeadingThe(nb) {
		return 0.98
	}
	if strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na) {
		return 0.9
	}
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return 0.78
	}
	ta, tb := strings.Fields(stripLeadingThe(na)), strings.Fields(stripLeadingThe(nb))
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	seen := make(map[string]bool, len(ta))
	for _, t := range ta {
		seen[t] = true
	}
	common := 0
	for _, t := range tb {
		if seen[t] {
			common++
		}
	}
	if common == 0 {
		return 0
	}
	union := len(ta) + len(tb) - common
	jaccard := float64(common) / float64(union)
	// Token overlap alone tops out at 0.75 so containment/equality always win.
	return 0.75 * jaccard
}

// ScoreRelease ranks a candidate for an album query.
func ScoreRelease(q ReleaseQuery, r Release) float64 {
	var total, weight float64
	add := func(w, sim float64) {
		total += w * sim
		weight += w
	}
	if q.Album != "" && r.Title != "" {
		add(0.55, Similarity(q.Album, r.Title))
	}
	if q.Artist != "" {
		artist := r.DisplayArtist()
		if artist == "" {
			artist = r.Artist.Name
		}
		sim := Similarity(q.Artist, artist)
		if r.Artist.Name != "" {
			if alt := Similarity(q.Artist, r.Artist.Name); alt > sim {
				sim = alt
			}
		}
		add(0.30, sim)
	}
	if q.Year > 0 && r.Year > 0 {
		if q.Year == r.Year {
			add(0.10, 1)
		} else {
			add(0.10, 0)
		}
	}
	if q.TrackCount > 0 && r.TrackCount > 0 {
		diff := q.TrackCount - r.TrackCount
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff == 0:
			add(0.05, 1)
		case diff <= 1:
			add(0.05, 0.6)
		default:
			add(0.05, 0)
		}
	}
	if weight == 0 {
		return 0
	}
	return total / weight
}

// RankReleases scores candidates and returns them best-first, trimmed to the
// query limit (default 10).
func RankReleases(q ReleaseQuery, releases []Release) []Release {
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	for i := range releases {
		releases[i].Score = ScoreRelease(q, releases[i])
	}
	sortReleases(releases)
	if len(releases) > limit {
		releases = releases[:limit]
	}
	return releases
}

// ScoreRecording ranks a candidate for a track query.
func ScoreRecording(q RecordingQuery, r Recording) float64 {
	var total, weight float64
	if q.Title != "" {
		total += 0.65 * Similarity(q.Title, r.Title)
		weight += 0.65
	}
	if q.Artist != "" {
		sim := Similarity(q.Artist, r.ArtistCredit)
		if r.Artist != "" {
			if alt := Similarity(q.Artist, r.Artist); alt > sim {
				sim = alt
			}
		}
		total += 0.35 * sim
		weight += 0.35
	}
	if weight == 0 {
		return 0
	}
	return total / weight
}

// RankRecordings scores candidates and returns them best-first, trimmed to the
// query limit (default 10).
func RankRecordings(q RecordingQuery, recs []Recording) []Recording {
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	for i := range recs {
		recs[i].Score = ScoreRecording(q, recs[i])
	}
	sortRecordings(recs)
	if len(recs) > limit {
		recs = recs[:limit]
	}
	return recs
}

func sortReleases(rs []Release) {
	// Stable sort keeps API/relevance order for equal scores and avoids the
	// O(n²) insertion sort copying fat structs over 1000-candidate sets.
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Score > rs[j].Score })
}

func sortRecordings(rs []Recording) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Score > rs[j].Score })
}
