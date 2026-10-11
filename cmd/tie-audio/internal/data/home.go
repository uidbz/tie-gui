package data

// home.go builds tie-audio's Home page: shelves of albums (and tracks) picked
// from the library listing and the play history — what is new, what was
// played lately, what sounds like it, what the user used to love but has not
// heard in a while. BuildHome is a pure function of its inputs (library,
// plays, clock, seed) so the ranking is unit-testable; Session.Home fetches
// the inputs in a handful of round trips: the album listing (which carries
// each album's genre/tags/added-at and play statistics via query Expand), the
// recent play events, the playlists, and one Expand for the recently played
// tracks.

import (
	"hash/fnv"
	"math"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uidbz/tie/client"
)

// Shelf IDs.
const (
	ShelfJumpBackIn    = "jump-back-in"
	ShelfRecentlyAdded = "recently-added"
	ShelfBecause       = "because"
	ShelfRediscover    = "rediscover"
	ShelfNeverPlayed   = "never-played"
	ShelfOnThisDay     = "on-this-day"
	ShelfFavorites     = "favorites"
	ShelfPlaylists     = "playlists"
	ShelfRecentTracks  = "recent-tracks"
)

// Home tuning.
const (
	shelfSize          = 12
	newWindow          = 30 * 24 * time.Hour
	seedWindow         = 14 * 24 * time.Hour
	recentlyHeard      = 7 * 24 * time.Hour
	rediscoverAfter    = 180 * 24 * time.Hour
	sessionGap         = 30 * time.Minute
	onThisDayDays      = 3
	minShelf           = 3 // shelves with fewer items are hidden
	homePlaysLimit     = 3000
	recentTracksShelf  = 12
	becauseSeedsShown  = 2
	becauseMinScore    = 0.5
	rediscoverMinPlays = 2
)

// Shelf is one row of the Home page.
type Shelf struct {
	ID       string
	Title    string
	Subtitle string
	Albums   []Album
	Tracks   []Track // track shelves (recently played tracks)
}

// Home is the Home page's content.
type Home struct {
	Shelves []Shelf
	Built   time.Time
	// ByUID indexes every album and playlist the page knows (resolving a
	// track tile's album).
	ByUID map[string]Album
}

// HomeInputs are BuildHome's inputs.
type HomeInputs struct {
	Library   []Album            // every album (LatestAlbums), with stats
	Playlists []Album            // AlbumPlaylist entries
	Plays     []client.PlayEvent // newest first
	Tracks    []Track            // recently played tracks, newest first
	Now       time.Time
	// Seed varies the random picks; derive it from the day so the page does
	// not reshuffle on every visit (DaySeed).
	Seed int64
}

// DaySeed is a stable seed for t's calendar day.
func DaySeed(t time.Time) int64 {
	h := fnv.New64a()
	h.Write([]byte(t.Format(time.DateOnly)))
	return int64(h.Sum64() >> 1)
}

// BuildHome ranks the shelves. Empty or near-empty shelves are left out, so a
// fresh library without history still gets "Recently added" and a sample.
func BuildHome(in HomeInputs) Home {
	h := Home{Built: in.Now}
	byUID := make(map[string]Album, len(in.Library)+len(in.Playlists))
	for _, a := range in.Library {
		byUID[a.UID] = a
	}
	for _, a := range in.Playlists {
		byUID[a.UID] = a
	}
	h.ByUID = byUID
	add := func(s Shelf) {
		need := minShelf
		if s.ID == ShelfJumpBackIn || s.ID == ShelfRecentTracks || s.ID == ShelfPlaylists {
			need = 1 // personal history: even one entry is worth a row
		}
		if len(s.Albums) >= need || len(s.Tracks) >= need {
			h.Shelves = append(h.Shelves, s)
		}
	}

	add(Shelf{ID: ShelfJumpBackIn, Title: "Jump back in", Albums: jumpBackIn(in.Plays, byUID)})
	if len(in.Tracks) > 0 {
		add(Shelf{ID: ShelfRecentTracks, Title: "Recently played tracks", Tracks: in.Tracks})
	}
	add(Shelf{ID: ShelfRecentlyAdded, Title: "Recently added", Albums: recentlyAdded(in.Library, in.Now)})
	for _, b := range becauseShelves(in, byUID) {
		add(b)
	}
	add(Shelf{ID: ShelfRediscover, Title: "Haven't heard in a while", Subtitle: "Albums you used to play",
		Albums: rediscover(in.Library, in.Now)})
	add(Shelf{ID: ShelfOnThisDay, Title: "On this day", Subtitle: "Played around this date in earlier years",
		Albums: onThisDay(in.Plays, byUID, in.Now)})
	add(Shelf{ID: ShelfFavorites, Title: "Favorites", Albums: favorites(in.Library, in.Seed)})
	add(Shelf{ID: ShelfNeverPlayed, Title: "Never played", Subtitle: "Waiting in your library",
		Albums: neverPlayed(in.Library, in.Now, in.Seed)})
	pl := slices.Clone(in.Playlists)
	if len(pl) > shelfSize {
		pl = pl[:shelfSize]
	}
	add(Shelf{ID: ShelfPlaylists, Title: "Your playlists", Albums: pl})
	return h
}

// jumpBackIn: the albums (and playlists) of the latest complete plays,
// most recent first.
func jumpBackIn(plays []client.PlayEvent, byUID map[string]Album) []Album {
	var out []Album
	seen := map[string]bool{}
	for _, p := range plays {
		if p.Outcome != client.OutcomeComplete || p.Collection == "" || seen[p.Collection] {
			continue
		}
		seen[p.Collection] = true
		if a, ok := byUID[p.Collection]; ok {
			out = append(out, a)
			if len(out) == shelfSize {
				break
			}
		}
	}
	return out
}

// recentlyAdded: albums added in the last 30 days, the unplayed ones first;
// a library with too few of those shows its newest albums instead.
func recentlyAdded(lib []Album, now time.Time) []Album {
	var fresh []Album
	for _, a := range lib {
		if !a.AddedAt.IsZero() && now.Sub(a.AddedAt) <= newWindow {
			fresh = append(fresh, a)
		}
	}
	if len(fresh) < minShelf*2 {
		fresh = fresh[:0]
		for _, a := range lib {
			if !a.AddedAt.IsZero() {
				fresh = append(fresh, a)
			}
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool {
		pi, pj := fresh[i].Stats.PlayCount == 0, fresh[j].Stats.PlayCount == 0
		if pi != pj {
			return pi
		}
		return fresh[i].AddedAt.After(fresh[j].AddedAt)
	})
	if len(fresh) > shelfSize*2 {
		fresh = fresh[:shelfSize*2]
	}
	return fresh
}

// sessions splits plays (any order) into listening sessions: runs of plays
// with gaps under 30 minutes. Each session is the set of collections played.
func sessions(plays []client.PlayEvent) []map[string]bool {
	ps := slices.Clone(plays)
	sort.Slice(ps, func(i, j int) bool { return ps[i].PlayedAt.Before(ps[j].PlayedAt) })
	var out []map[string]bool
	var cur map[string]bool
	var last time.Time
	for _, p := range ps {
		if p.Outcome != client.OutcomeComplete || p.Collection == "" {
			continue
		}
		if cur == nil || p.PlayedAt.Sub(last) > sessionGap {
			cur = map[string]bool{}
			out = append(out, cur)
		}
		cur[p.Collection] = true
		last = p.PlayedAt
	}
	return out
}

// becauseShelves: for the 1–2 albums played most in the last two weeks, the
// albums that sound related — played in the same listening sessions, same
// album artist, shared genres or tags, a similar era — minus what was heard
// in the last week.
func becauseShelves(in HomeInputs, byUID map[string]Album) []Shelf {
	counts := map[string]int{}
	for _, p := range in.Plays {
		if p.Outcome == client.OutcomeComplete && p.Collection != "" && in.Now.Sub(p.PlayedAt) <= seedWindow {
			if a, ok := byUID[p.Collection]; ok && a.Kind != AlbumPlaylist {
				counts[p.Collection]++
			}
		}
	}
	seeds := make([]string, 0, len(counts))
	for uid := range counts {
		seeds = append(seeds, uid)
	}
	sort.Slice(seeds, func(i, j int) bool {
		if counts[seeds[i]] != counts[seeds[j]] {
			return counts[seeds[i]] > counts[seeds[j]]
		}
		return seeds[i] < seeds[j]
	})
	if len(seeds) > becauseSeedsShown {
		seeds = seeds[:becauseSeedsShown]
	}
	recent := map[string]bool{}
	for _, p := range in.Plays {
		if in.Now.Sub(p.PlayedAt) <= recentlyHeard && p.Collection != "" {
			recent[p.Collection] = true
		}
	}
	sess := sessions(in.Plays)
	var out []Shelf
	used := map[string]bool{}
	for _, uid := range seeds {
		seed := byUID[uid]
		co := map[string]int{}
		maxCo := 0
		for _, s := range sess {
			if !s[uid] {
				continue
			}
			for other := range s {
				if other != uid {
					co[other]++
					maxCo = max(maxCo, co[other])
				}
			}
		}
		type scored struct {
			a     Album
			score float64
		}
		var cands []scored
		for _, a := range in.Library {
			if a.UID == uid || recent[a.UID] || used[a.UID] || slices.Contains(seeds, a.UID) {
				continue
			}
			sc := relatedness(seed, a)
			if maxCo > 0 {
				sc += 3 * float64(co[a.UID]) / float64(maxCo)
			}
			sc += 0.2 * math.Log1p(float64(a.Stats.PlayCount))
			if n := a.Stats.PlayCount + a.Stats.SkipCount; n > 0 {
				sc -= 0.5 * float64(a.Stats.SkipCount) / float64(n)
			}
			if sc >= becauseMinScore {
				cands = append(cands, scored{a, sc})
			}
		}
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].a.UID < cands[j].a.UID
		})
		var albums []Album
		for _, c := range cands {
			albums = append(albums, c.a)
			used[c.a.UID] = true
			if len(albums) == shelfSize {
				break
			}
		}
		out = append(out, Shelf{ID: ShelfBecause + ":" + uid, Title: "Because you played " + seed.Display(), Albums: albums})
	}
	return out
}

// relatedness scores two albums by their metadata alone.
func relatedness(a, b Album) float64 {
	sc := 0.0
	if a.Artist != "" && strings.EqualFold(a.Artist, b.Artist) && !strings.EqualFold(a.Artist, "Various Artists") {
		sc += 2
	}
	sc += 1.5 * jaccard(a.Genres, b.Genres)
	sc += jaccard(withoutReserved(a.Tags), withoutReserved(b.Tags))
	ya, ea := strconv.Atoi(a.Year)
	yb, eb := strconv.Atoi(b.Year)
	if ea == nil && eb == nil && ya > 0 && yb > 0 {
		if d := math.Abs(float64(ya - yb)); d <= 5 {
			sc += 0.5 * (1 - d/5)
		}
	}
	return sc
}

func withoutReserved(tags []string) []string {
	var out []string
	for _, t := range tags {
		if t != PlaylistTag {
			out = append(out, t)
		}
	}
	return out
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, x := range a {
		set[strings.ToLower(x)] = true
	}
	inter := 0
	union := len(set)
	seen := map[string]bool{}
	for _, y := range b {
		k := strings.ToLower(y)
		if seen[k] {
			continue
		}
		seen[k] = true
		if set[k] {
			inter++
		} else {
			union++
		}
	}
	return float64(inter) / float64(union)
}

// rediscover: albums played at least twice (or marked favorite) whose last
// play is more than six months ago, the most-loved and longest-missed first.
func rediscover(lib []Album, now time.Time) []Album {
	type scored struct {
		a     Album
		score float64
	}
	var cands []scored
	for _, a := range lib {
		fav := slices.Contains(a.Tags, "favorite")
		if a.Stats.LastPlayed.IsZero() || (a.Stats.PlayCount < rediscoverMinPlays && !fav) {
			continue
		}
		since := now.Sub(a.Stats.LastPlayed)
		if since < rediscoverAfter {
			continue
		}
		days := since.Hours() / 24
		sc := math.Log1p(float64(a.Stats.PlayCount)) * (1 - math.Exp(-days/180))
		if fav {
			sc += 1
		}
		cands = append(cands, scored{a, sc})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	out := make([]Album, 0, shelfSize)
	for _, c := range cands {
		out = append(out, c.a)
		if len(out) == shelfSize {
			break
		}
	}
	return out
}

// onThisDay: albums played within ±3 days of today's date in earlier years.
func onThisDay(plays []client.PlayEvent, byUID map[string]Album, now time.Time) []Album {
	var out []Album
	seen := map[string]bool{}
	for _, p := range plays {
		if p.Outcome != client.OutcomeComplete || p.Collection == "" || seen[p.Collection] {
			continue
		}
		if p.PlayedAt.Year() >= now.Year() {
			continue
		}
		anniversary := time.Date(now.Year(), p.PlayedAt.Month(), p.PlayedAt.Day(), 0, 0, 0, 0, now.Location())
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if d := math.Abs(anniversary.Sub(today).Hours() / 24); d > onThisDayDays {
			continue
		}
		if a, ok := byUID[p.Collection]; ok {
			seen[p.Collection] = true
			out = append(out, a)
			if len(out) == shelfSize {
				break
			}
		}
	}
	return out
}

// favorites: albums tagged favorite, in a daily random order.
func favorites(lib []Album, seed int64) []Album {
	var out []Album
	for _, a := range lib {
		if slices.Contains(a.Tags, "favorite") {
			out = append(out, a)
		}
	}
	shuffle(out, seed)
	if len(out) > shelfSize {
		out = out[:shelfSize]
	}
	return out
}

// neverPlayed: albums in the library for over a month that were never played
// (nor skipped), a daily random sample.
func neverPlayed(lib []Album, now time.Time, seed int64) []Album {
	var out []Album
	for _, a := range lib {
		if a.Stats.PlayCount == 0 && a.Stats.SkipCount == 0 && a.Stats.LastPlayed.IsZero() && !a.AddedAt.IsZero() && now.Sub(a.AddedAt) > newWindow {
			out = append(out, a)
		}
	}
	shuffle(out, seed+1)
	if len(out) > shelfSize {
		out = out[:shelfSize]
	}
	return out
}

func shuffle(as []Album, seed int64) {
	sort.SliceStable(as, func(i, j int) bool { return as[i].UID < as[j].UID }) // deterministic base
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(as), func(i, j int) { as[i], as[j] = as[j], as[i] })
}

// PlaylistAlbums lists the collection's playlists as albums.
func (s *Session) PlaylistAlbums() ([]Album, error) {
	ps, err := s.Tie.ListPlaylists()
	if err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(ps))
	for _, p := range ps {
		out = append(out, Album{
			UID: p.UID, Kind: AlbumPlaylist, Title: p.Name, ThumbHash: p.Thumbnail,
			Tags: p.Tags, AddedAt: p.AddedAt, TrackCount: len(p.Tracks),
		})
	}
	s.rememberThumbs(out)
	return out, nil
}

// Home fetches the Home page's inputs and builds it.
func (s *Session) Home(now time.Time) (Home, error) {
	lib, err := s.LatestAlbums()
	if err != nil {
		return Home{}, err
	}
	plays, err := s.RecentPlays(time.Time{}, homePlaysLimit)
	if err != nil {
		return Home{}, err
	}
	playlists, err := s.PlaylistAlbums()
	if err != nil {
		playlists = nil // a playlist failure should not blank the page
	}
	return BuildHome(HomeInputs{
		Library: lib, Playlists: playlists, Plays: plays,
		Tracks: s.recentTracks(plays), Now: now, Seed: DaySeed(now),
	}), nil
}

// recentTracks resolves the latest distinct completely played tracks (one
// Expand), attributing each to the collection it was played from.
func (s *Session) recentTracks(plays []client.PlayEvent) []Track {
	var hashes []string
	from := map[string]string{}
	for _, p := range plays {
		if p.Outcome != client.OutcomeComplete {
			continue
		}
		if _, ok := from[p.Subject]; ok {
			continue
		}
		from[p.Subject] = p.Collection
		hashes = append(hashes, p.Subject)
		if len(hashes) == recentTracksShelf {
			break
		}
	}
	if len(hashes) == 0 {
		return nil
	}
	rows, err := s.Tie.Expand(hashes)
	if err != nil {
		return nil
	}
	byHash := map[string]client.Row{}
	for _, r := range rows {
		byHash[r.Key] = r
	}
	var out []Track
	for _, h := range hashes {
		r, ok := byHash[h]
		if !ok {
			continue
		}
		t := trackFromRow(r, "")
		if c := from[h]; c != "" {
			t.AlbumUID = c
		}
		out = append(out, t)
	}
	return out
}
