package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webProvider talks to the MusicBrainz web service.
type webProvider struct {
	base   string
	ua     string
	client *http.Client

	interval   time.Duration
	retryDelay time.Duration
}

// webPacer enforces the ~1 request/second MusicBrainz guideline across ALL
// webProvider instances, keyed by base URL. The UI opens a fresh provider per
// settings dialog, album scan, or manual lookup, so a per-instance limiter
// would let concurrent scans collectively exceed the public API's rate ceiling
// — which it punishes with immediate 429/503s.
type webPacer struct {
	mu   sync.Mutex
	next map[string]time.Time
}

var pacer = &webPacer{next: map[string]time.Time{}}

func (p *webPacer) wait(ctx context.Context, base string, interval time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := time.Until(p.next[base]); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	p.next[base] = time.Now().Add(interval)
	return nil
}

func openWeb(s Settings) (Provider, error) {
	if s.WebBaseURL == "" {
		return nil, fmt.Errorf("musicbrainz: empty web base URL")
	}
	return &webProvider{
		base:       s.WebBaseURL,
		ua:         s.UserAgent,
		client:     &http.Client{Timeout: s.Timeout},
		interval:   time.Second,
		retryDelay: 2 * time.Second,
	}, nil
}

func (w *webProvider) Kind() string { return SourceWeb }

func (w *webProvider) Close() error {
	w.client.CloseIdleConnections()
	return nil
}

func (w *webProvider) Ping(ctx context.Context) error {
	_, err := w.SearchReleases(ctx, ReleaseQuery{Album: "abbey road", Limit: 1})
	return err
}

// wait enforces the MusicBrainz ~1 request/second guideline (shared across
// provider instances via pacer).
func (w *webProvider) wait(ctx context.Context) error {
	return pacer.wait(ctx, w.base, w.interval)
}

func (w *webProvider) getJSON(ctx context.Context, path string, params url.Values, out any) error {
	if err := w.wait(ctx); err != nil {
		return err
	}
	u := w.base + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", w.ua)
	req.Header.Set("Accept", "application/json")

	// The public service occasionally answers 503/429 under load; retry once
	// after a short pause rather than failing the whole lookup.
	for attempt := 0; ; attempt++ {
		resp, err := w.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				return fmt.Errorf("musicbrainz: decode %s: %w", path, err)
			}
			return nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		if !retryable || attempt >= 2 {
			return fmt.Errorf("musicbrainz: %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * w.retryDelay):
		}
		if err := w.wait(ctx); err != nil {
			return err
		}
	}
}

// --- web service JSON shapes (only the fields we use) ---

type wsArtistCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

type wsReleaseGroup struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
	FirstRelease   string   `json:"first-release-date"`
}

type wsMedium struct {
	Position   int       `json:"position"`
	Format     string    `json:"format"`
	TrackCount int       `json:"track-count"`
	Tracks     []wsTrack `json:"tracks"`
}

type wsTrack struct {
	ID        string           `json:"id"`
	Title     string           `json:"title"`
	Number    string           `json:"number"`
	Position  int              `json:"position"`
	Length    int              `json:"length"`
	Recording *wsRecordingRef  `json:"recording"`
	Credits   []wsArtistCredit `json:"artist-credit"`
}

type wsRecordingRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type wsRelease struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Date         string           `json:"date"`
	Country      string           `json:"country"`
	Status       string           `json:"status"`
	Credits      []wsArtistCredit `json:"artist-credit"`
	ReleaseGroup *wsReleaseGroup  `json:"release-group"`
	Media        []wsMedium       `json:"media"`
	TrackCount   int              `json:"track-count"`
	Genres       []wsTag          `json:"genres"`
	Tags         []wsTag          `json:"tags"`
	CoverArt     *struct {
		Front bool `json:"front"`
	} `json:"cover-art-archive"`
	Score int `json:"score"`
}

type wsTag struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type wsReleaseSearch struct {
	Releases []wsRelease `json:"releases"`
}

type wsRecording struct {
	ID       string           `json:"id"`
	Title    string           `json:"title"`
	Length   int              `json:"length"`
	Credits  []wsArtistCredit `json:"artist-credit"`
	Releases []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ReleaseGroup *struct {
			PrimaryType string `json:"primary-type"`
		} `json:"release-group"`
	} `json:"releases"`
	Score int `json:"score"`
}

type wsRecordingSearch struct {
	Recordings []wsRecording `json:"recordings"`
}

// --- conversions ---

func creditsName(credits []wsArtistCredit) string {
	if len(credits) == 0 {
		return ""
	}
	if credits[0].Name != "" {
		// MB repeats the full credit (with join phrases) as the first entry's
		// name; use it when it is the only entry, otherwise join names.
		if len(credits) == 1 {
			return credits[0].Name
		}
	}
	parts := make([]string, 0, len(credits))
	for _, c := range credits {
		n := c.Name
		if n == "" {
			n = c.Artist.Name
		}
		if n != "" {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, " & ")
}

func firstArtist(credits []wsArtistCredit) Artist {
	for _, c := range credits {
		if c.Artist.ID != "" || c.Artist.Name != "" {
			return Artist{MBID: c.Artist.ID, Name: c.Artist.Name}
		}
	}
	return Artist{}
}

func releaseFromWS(r wsRelease) Release {
	out := Release{
		MBID:         r.ID,
		Title:        r.Title,
		Artist:       firstArtist(r.Credits),
		ArtistCredit: creditsName(r.Credits),
		Date:         r.Date,
		Year:         ParseYear(r.Date),
		Country:      r.Country,
		Status:       r.Status,
	}
	if r.ReleaseGroup != nil {
		out.ReleaseGroupMBID = r.ReleaseGroup.ID
		out.ReleaseGroupTitle = r.ReleaseGroup.Title
		out.PrimaryType = r.ReleaseGroup.PrimaryType
		out.SecondaryTypes = append([]string(nil), r.ReleaseGroup.SecondaryTypes...)
		if out.Year == 0 {
			out.Year = ParseYear(r.ReleaseGroup.FirstRelease)
		}
	}
	for _, g := range r.Genres {
		if g.Name != "" {
			out.Genres = append(out.Genres, g.Name)
		}
	}
	for _, t := range r.Tags {
		if t.Name != "" && t.Count > 0 {
			out.Tags = append(out.Tags, t.Name)
		}
	}
	if r.CoverArt != nil {
		out.HasCoverArt = r.CoverArt.Front
	}
	out.TrackCount = r.TrackCount
	for _, m := range r.Media {
		out.Mediums = append(out.Mediums, mediumFromWS(m, len(m.Tracks) > 0))
	}
	if out.TrackCount == 0 {
		for _, m := range out.Mediums {
			out.TrackCount += m.TrackCount
		}
	}
	if len(out.Mediums) > 0 {
		out.DiscCount = len(out.Mediums)
	} else if out.TrackCount > 0 {
		out.DiscCount = 1
	}
	return out
}

// mediumFromWS converts one medium. full controls whether track objects are
// parsed (lookup) or only the track count is used (search).
func mediumFromWS(m wsMedium, full bool) Medium {
	med := Medium{Position: m.Position, Format: m.Format, TrackCount: m.TrackCount}
	if med.TrackCount == 0 {
		med.TrackCount = len(m.Tracks)
	}
	if !full {
		return med
	}
	for _, t := range m.Tracks {
		tr := Track{
			MBID:         t.ID,
			Title:        t.Title,
			Artist:       creditsName(t.Credits),
			ArtistCredit: creditsName(t.Credits),
			Position:     t.Position,
			Number:       t.Number,
			Disc:         m.Position,
			LengthMS:     t.Length,
		}
		if t.Recording != nil {
			tr.RecordingMBID = t.Recording.ID
			if tr.Title == "" {
				tr.Title = t.Recording.Title
			}
		}
		med.Tracks = append(med.Tracks, tr)
	}
	med.TrackCount = len(med.Tracks)
	return med
}

// releaseFromWSLookup converts a lookup response, whose media always carry
// full track objects.
func releaseFromWSLookup(r wsRelease) Release {
	out := releaseFromWS(r)
	out.TrackCount = 0
	for _, m := range out.Mediums {
		out.TrackCount += m.TrackCount
	}
	if len(out.Mediums) > 0 {
		out.DiscCount = len(out.Mediums)
	}
	return out
}

func luceneEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func (w *webProvider) SearchReleases(ctx context.Context, q ReleaseQuery) ([]Release, error) {
	var terms []string
	if strings.TrimSpace(q.Album) != "" {
		terms = append(terms, fmt.Sprintf(`release:"%s"`, luceneEscape(q.Album)))
	}
	if strings.TrimSpace(q.Artist) != "" {
		terms = append(terms, fmt.Sprintf(`artist:"%s"`, luceneEscape(q.Artist)))
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("musicbrainz: empty release query")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	// Fetch a wider candidate set: the API's relevance order does not know
	// about year/track-count, and several editions of the same album match.
	fetch := limit * 3
	if fetch < 25 {
		fetch = 25
	}
	if fetch > 100 {
		fetch = 100
	}
	params := url.Values{}
	params.Set("query", strings.Join(terms, " AND "))
	params.Set("fmt", "json")
	params.Set("limit", strconv.Itoa(fetch))
	var res wsReleaseSearch
	if err := w.getJSON(ctx, "/ws/2/release", params, &res); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(res.Releases))
	for _, r := range res.Releases {
		out = append(out, releaseFromWS(r))
	}
	// Re-score locally too: the API's score is relevance, ours accounts for
	// year and track count. The API order is a useful tiebreaker only.
	for i := range out {
		out[i].Score = ScoreRelease(q, out[i])
	}
	sortReleases(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (w *webProvider) LookupRelease(ctx context.Context, mbid string) (*Release, error) {
	if strings.TrimSpace(mbid) == "" {
		return nil, fmt.Errorf("musicbrainz: empty release MBID")
	}
	params := url.Values{}
	// cover-art-archive is returned by default on release lookups; passing it
	// as an inc value is rejected by the web service.
	params.Set("inc", "recordings+artist-credits+release-groups+genres+tags+media+labels")
	params.Set("fmt", "json")
	var res wsRelease
	if err := w.getJSON(ctx, "/ws/2/release/"+url.PathEscape(mbid), params, &res); err != nil {
		return nil, err
	}
	out := releaseFromWSLookup(res)
	return &out, nil
}

func (w *webProvider) SearchRecordings(ctx context.Context, q RecordingQuery) ([]Recording, error) {
	var terms []string
	if strings.TrimSpace(q.Title) != "" {
		terms = append(terms, fmt.Sprintf(`recording:"%s"`, luceneEscape(q.Title)))
	}
	if strings.TrimSpace(q.Artist) != "" {
		terms = append(terms, fmt.Sprintf(`artist:"%s"`, luceneEscape(q.Artist)))
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("musicbrainz: empty recording query")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	fetch := limit * 2
	if fetch < 25 {
		fetch = 25
	}
	if fetch > 100 {
		fetch = 100
	}
	params := url.Values{}
	params.Set("query", strings.Join(terms, " AND "))
	params.Set("fmt", "json")
	params.Set("limit", strconv.Itoa(fetch))
	var res wsRecordingSearch
	if err := w.getJSON(ctx, "/ws/2/recording", params, &res); err != nil {
		return nil, err
	}
	out := make([]Recording, 0, len(res.Recordings))
	for _, r := range res.Recordings {
		rec := Recording{
			MBID:         r.ID,
			Title:        r.Title,
			Artist:       firstArtist(r.Credits).Name,
			ArtistMBID:   firstArtist(r.Credits).MBID,
			ArtistCredit: creditsName(r.Credits),
			LengthMS:     r.Length,
		}
		for _, rel := range r.Releases {
			rec.Releases = append(rec.Releases, RecordingRelease{
				MBID:  rel.ID,
				Title: rel.Title,
				Year:  ParseYear(rel.Date),
			})
		}
		out = append(out, rec)
	}
	out = RankRecordings(q, out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
