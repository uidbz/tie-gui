package data

import (
	"sort"

	"github.com/uidbz/tie/client"
)

// SearchResult is a free-text search's album wall plus the total number of
// matching subjects before paging. Tracks surface as standalone AlbumTrack
// tiles or fold into their album, exactly as a tag query's wall does.
type SearchResult struct {
	Albums []Album
	Total  int
}

// searchPageSize is deliberately larger than the wall's visible tiles: a query
// on an artist or album matches every track in it, and grouping those hits into
// album tiles must still leave a representative page of albums.
const searchPageSize = 400

// Search runs the audio profile's free-text search and classifies the matching
// rows into wall albums. Album rows are fetched for track hits so an album that
// matched only through its tracks still renders with its own metadata; the
// result keeps the server's rank, with the first matching row of an album
// deciding its position.
func (s *Session) Search(query string, limit int) (SearchResult, error) {
	spec := client.AudioSearchProfile.Spec(query)
	spec.Expand = true
	if limit <= 0 {
		limit = searchPageSize
	}
	spec.Limit = limit

	matches, total, err := s.Tie.Search(spec)
	if err != nil {
		return SearchResult{}, err
	}
	if len(matches) == 0 {
		return SearchResult{Total: total}, nil
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
		albumRows, err := s.Tie.Expand(extra)
		if err != nil {
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
	return SearchResult{Albums: albums, Total: total}, nil
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
