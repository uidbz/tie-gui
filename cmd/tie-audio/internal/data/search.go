package data

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/uidbz/tie/client"
)

// SearchResult is a free-text search's album wall plus the total number of
// matching subjects before paging. Tracks surface as standalone AlbumTrack
// tiles or fold into their album, exactly as a tag query's wall does.
type SearchResult struct {
	Albums []Album
	Total  int
	// Revision is the server-side search-index revision this page was
	// computed from. Paging across writes compares it between pages.
	Revision uint64
}

// searchPageSize is deliberately larger than the wall's visible tiles: a query
// on an artist or album matches every track in it, and grouping those hits into
// album tiles must still leave a representative page of albums.
const searchPageSize = 400

// SearchScope narrows free-text search to a kind of subject.
type SearchScope int

const (
	// SearchAll covers albums (audio dirs and archives) and tracks.
	SearchAll SearchScope = iota
	// SearchAlbumsOnly matches album/playlist names (audio-dir and
	// audio-archive subjects) — a track hit never pulls its album in.
	SearchAlbumsOnly
	// SearchTracksOnly matches track-level fields (title/artist/genre/…);
	// the hits surface as tracks, not folded into album tiles.
	SearchTracksOnly
)

// SearchOptions tunes one free-text search; the zero value is the audio
// profile's default (ranked word match over the standard vocabulary).
type SearchOptions struct {
	// Mode selects the matching algorithm: ranked words (default),
	// substring, prefix or fuzzy (typo-tolerant) matching.
	Mode client.SearchMode
	// Fields restricts which relations are searched (nil = the audio
	// profile's full vocabulary: title, album, album-artist, artist,
	// genre, tags, filename).
	Fields []client.Relation
	// Scope narrows the candidate subjects.
	Scope SearchScope
}

// searchSpec builds the client spec for a query (pure, so the mapping is
// unit-testable without a triplestore).
func searchSpec(query string, limit int, opts SearchOptions) client.SearchSpec {
	spec := client.AudioSearchProfile.Spec(query)
	spec.Expand = true
	if limit <= 0 {
		limit = searchPageSize
	}
	spec.Limit = limit
	if opts.Mode != "" {
		spec.Mode = opts.Mode
	}
	if len(opts.Fields) > 0 {
		spec.Relations = opts.Fields
	}
	switch opts.Scope {
	case SearchAlbumsOnly:
		spec.TieTypes = []client.TieType{client.TieAudioDir, client.TieAudioArchive}
	case SearchTracksOnly:
		spec.TieTypes = []client.TieType{client.TieAudioFile}
	}
	return spec
}

// Search runs the audio profile's free-text search and classifies the matching
// rows into wall albums. Album rows are fetched for track hits so an album that
// matched only through its tracks still renders with its own metadata; the
// result keeps the server's rank, with the first matching row of an album
// deciding its position.
func (s *Session) Search(query string, limit int) (SearchResult, error) {
	return s.SearchEx(context.Background(), query, SearchOptions{}, limit)
}

// SearchEx runs a free-text search with explicit options and a cancellable
// context (a GUI that moved on from a query stops waiting instead of running
// it to completion).
func (s *Session) SearchEx(ctx context.Context, query string, opts SearchOptions, limit int) (SearchResult, error) {
	res, err := s.Tie.SearchPageContext(ctx, searchSpec(query, limit, opts))
	if err != nil {
		return SearchResult{}, err
	}
	matches, total := res.Matches, res.Total
	if len(matches) == 0 {
		return SearchResult{Total: total, Revision: res.Revision}, nil
	}

	rows := make([]client.Row, 0, len(matches)+16)
	seen := make(map[string]bool, len(matches)+16)
	rank := make(map[string]int, len(matches)+16)
	rankOf := func(key string, i int) {
		if _, ok := rank[key]; !ok {
			rank[key] = i
		}
	}
	var extra []string
	extraSeen := make(map[string]bool)

	for i, m := range matches {
		rows = append(rows, client.Row{Key: m.Key, Attributes: m.Attributes})
		seen[m.Key] = true
		rankOf(m.Key, i)
		for _, relation := range []client.Relation{client.RelMemberOf, client.RelParent} {
			for _, parent := range m.Attributes[string(relation)] {
				rankOf(parent, i)
				if parent == "" || seen[parent] || extraSeen[parent] {
					continue
				}
				extraSeen[parent] = true
				extra = append(extra, parent)
			}
		}
	}

	if len(extra) > 0 {
		albumRows, err := s.Tie.ExpandContext(ctx, extra)
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return SearchResult{}, err
		}
		for _, row := range albumRows {
			if seen[row.Key] {
				continue
			}
			seen[row.Key] = true
			rows = append(rows, row)
		}
	}

	albums := s.albumsFromRows(rows)
	position := func(uid string) int {
		if i, ok := rank[uid]; ok {
			return i
		}
		return len(matches) + 1
	}
	sort.SliceStable(albums, func(i, j int) bool {
		return position(albums[i].UID) < position(albums[j].UID)
	})
	return SearchResult{Albums: albums, Total: total, Revision: res.Revision}, nil
}

// SearchTracks runs a track-scoped free-text search and returns the matching
// tracks in the server's relevance order — the track-level view of the same
// engine the album wall uses. Track.Album is backfilled from the owning
// album's own triples when the track row carries no album name.
func (s *Session) SearchTracks(ctx context.Context, query string, opts SearchOptions) ([]Track, int, error) {
	opts.Scope = SearchTracksOnly
	res, err := s.Tie.SearchPageContext(ctx, searchSpec(query, searchPageSize, opts))
	if err != nil {
		return nil, 0, err
	}
	tracks := make([]Track, 0, len(res.Matches))
	for _, m := range res.Matches {
		tracks = append(tracks, trackFromMatch(m))
	}

	// One Expand for the album display names of tracks whose own row lacks
	// the album attribute (imported before album summaries were indexed).
	missing := make([]string, 0, len(tracks))
	seen := make(map[string]bool, len(tracks))
	for _, t := range tracks {
		if t.Album != "" || t.AlbumUID == "" || t.AlbumUID == t.Hash || seen[t.AlbumUID] {
			continue
		}
		seen[t.AlbumUID] = true
		missing = append(missing, t.AlbumUID)
	}
	if len(missing) > 0 {
		rows, err := s.Tie.ExpandContext(ctx, missing)
		if err != nil && !errors.Is(err, client.ErrNotFound) {
			return nil, 0, err
		}
		name := map[string]string{}
		for _, row := range rows {
			n := client.RowFirst(row, client.RelAlbum)
			if n == "" {
				n = client.RowFirst(row, client.RelName)
			}
			name[row.Key] = n
		}
		for i := range tracks {
			if n := name[tracks[i].AlbumUID]; n != "" {
				tracks[i].Album = n
			}
		}
	}
	return tracks, res.Total, nil
}

// trackFromMatch turns one expanded search match into a Track (pure; the
// match's Attributes came from SearchSpec.Expand).
func trackFromMatch(m client.SearchMatch) Track {
	row := client.Row{Key: m.Key, Attributes: m.Attributes}
	atof := func(rel client.Relation) float64 {
		f, _ := strconv.ParseFloat(client.RowFirst(row, rel), 64)
		return f
	}
	atoi := func(rel client.Relation) int {
		n, _ := strconv.Atoi(client.RowFirst(row, rel))
		return n
	}
	t := Track{
		Hash:     m.Key,
		Title:    client.RowFirst(row, client.RelTitle),
		Artist:   client.RowFirst(row, client.RelArtist),
		Album:    client.RowFirst(row, client.RelAlbum),
		Year:     client.RowFirst(row, client.RelYear),
		TrackNo:  atoi(client.RelTrack),
		Disc:     atoi(client.RelDisc),
		Filename: client.RowFirst(row, client.RelFilename),
		Duration: atof(client.RelDuration),
	}
	// Same album-resolution order as the album wall: membership first
	// (playlists/disc folders keep one parent), then the filing dir, then
	// the hash itself (standalone track).
	t.AlbumUID = firstAttr(row, client.RelMemberOf)
	if t.AlbumUID == "" {
		t.AlbumUID = firstAttr(row, client.RelParent)
	}
	if t.AlbumUID == "" {
		t.AlbumUID = m.Key
	}
	if t.Title == "" {
		t.Title = t.Filename
	}
	return t
}

func firstAttr(row client.Row, rel client.Relation) string {
	if vals := client.RowValues(row, rel); len(vals) > 0 {
		return vals[0]
	}
	return ""
}

// SearchWithin filters a search result to albums whose UID is in allowed. It is
// the "search in current selection" path: the wall already holds the tag or
// directory selection, so no second server query is needed.
func SearchWithin(result SearchResult, allowed map[string]bool) SearchResult {
	if len(allowed) == 0 {
		return SearchResult{Total: 0}
	}
	albums := make([]Album, 0, len(result.Albums))
	for _, a := range result.Albums {
		if allowed[a.UID] {
			albums = append(albums, a)
		}
	}
	return SearchResult{Albums: albums, Total: len(albums)}
}
