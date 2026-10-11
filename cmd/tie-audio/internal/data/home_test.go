package data

import (
	"slices"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

func shelfByID(h Home, id string) *Shelf {
	for i := range h.Shelves {
		if h.Shelves[i].ID == id || (id == ShelfBecause && len(h.Shelves[i].ID) > len(id) && h.Shelves[i].ID[:len(id)] == id) {
			return &h.Shelves[i]
		}
	}
	return nil
}

func uids(as []Album) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.UID
	}
	return out
}

func TestBuildHome(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	alb := func(uid, artist, year string, genres []string, added time.Duration, plays int, last time.Duration, tags ...string) Album {
		a := Album{UID: uid, Kind: AlbumDir, Title: uid, Artist: artist, Year: year, Genres: genres, Tags: tags, AddedAt: now.Add(-added)}
		a.Stats.PlayCount = plays
		if last > 0 {
			a.Stats.LastPlayed = now.Add(-last)
		}
		return a
	}
	lib := []Album{
		alb("seed", "Miles", "1959", []string{"Jazz"}, 400*day, 10, 1*day),
		alb("same-artist", "Miles", "1961", []string{"Jazz"}, 400*day, 1, 30*day),
		alb("colisten", "Other", "1990", []string{"Rock"}, 400*day, 3, 20*day),
		alb("genre", "Coltrane", "1960", []string{"Jazz"}, 400*day, 0, 0),
		alb("unrelated", "Pop", "2020", []string{"Pop"}, 400*day, 0, 0),
		alb("heard-yesterday", "Miles", "1958", []string{"Jazz"}, 400*day, 5, 1*day),
		alb("old-love", "X", "1980", nil, 900*day, 30, 400*day),
		alb("old-fav", "Y", "1981", nil, 900*day, 0, 300*day, "favorite"),
		alb("new1", "N", "2026", nil, 2*day, 0, 0),
		alb("new2", "N", "2026", nil, 3*day, 1, 1*day),
		alb("new3", "N", "2026", nil, 4*day, 0, 0),
	}
	lib[4].Stats.SkipCount = 1 // "unrelated" was skipped once
	var plays []client.PlayEvent
	ev := func(coll string, at time.Duration, outcome client.PlayOutcome) {
		plays = append(plays, client.PlayEvent{Subject: coll + "-t", Collection: coll, PlayedAt: now.Add(-at), Outcome: outcome})
	}
	// Seed played a lot lately; colisten shares an old session with it.
	for i := 0; i < 5; i++ {
		ev("seed", time.Duration(i+1)*day, client.OutcomeComplete)
	}
	ev("seed", 20*day, client.OutcomeComplete)
	ev("colisten", 20*day-10*time.Minute, client.OutcomeComplete)
	ev("heard-yesterday", 1*day+time.Hour, client.OutcomeComplete)
	ev("new2", 1*day+2*time.Hour, client.OutcomeComplete)
	ev("unrelated", 2*day, client.OutcomeSkip)
	// A year ago today.
	ev("old-love", 365*day+day, client.OutcomeComplete)
	sortPlays := func() {
		slices.SortFunc(plays, func(a, b client.PlayEvent) int { return b.PlayedAt.Compare(a.PlayedAt) })
	}
	sortPlays()

	h := BuildHome(HomeInputs{Library: lib, Plays: plays, Now: now, Seed: 1})

	jb := shelfByID(h, ShelfJumpBackIn)
	if jb == nil || !slices.Equal(uids(jb.Albums)[:3], []string{"seed", "heard-yesterday", "new2"}) {
		t.Fatalf("jump back in: %+v", jb)
	}
	if slices.Contains(uids(jb.Albums), "unrelated") {
		t.Error("a skip counted as a play")
	}

	ra := shelfByID(h, ShelfRecentlyAdded)
	if ra == nil || !slices.Equal(uids(ra.Albums)[:2], []string{"new1", "new3"}) {
		t.Fatalf("recently added (unplayed first): %+v", ra)
	}

	b := shelfByID(h, ShelfBecause)
	if b == nil || b.Title != "Because you played seed" {
		t.Fatalf("because: %+v", h.Shelves)
	}
	got := uids(b.Albums)
	if got[0] != "colisten" && got[0] != "same-artist" {
		t.Errorf("because ranking: %v", got)
	}
	for _, want := range []string{"same-artist", "colisten", "genre"} {
		if !slices.Contains(got, want) {
			t.Errorf("because missing %s: %v", want, got)
		}
	}
	for _, not := range []string{"heard-yesterday", "seed", "unrelated"} {
		if slices.Contains(got, not) {
			t.Errorf("because includes %s: %v", not, got)
		}
	}

	rd := shelfByID(h, ShelfRediscover)
	if rd != nil {
		t.Errorf("rediscover shown with only %d items", len(rd.Albums))
	}
	if r := rediscover(lib, now); !slices.Equal(uids(r), []string{"old-love", "old-fav"}) {
		t.Errorf("rediscover: %v", uids(r))
	}
	if o := onThisDay(plays, map[string]Album{"old-love": lib[6]}, now); len(o) != 1 || o[0].UID != "old-love" {
		t.Errorf("on this day: %v", uids(o))
	}
	np := neverPlayed(lib, now, 1)
	if !slices.Contains(uids(np), "genre") || slices.Contains(uids(np), "new1") || slices.Contains(uids(np), "unrelated") {
		t.Errorf("never played: %v", uids(np))
	}
	// Same seed, same picks; the page is stable within a day.
	if !slices.Equal(uids(neverPlayed(lib, now, 7)), uids(neverPlayed(lib, now, 7))) {
		t.Error("never played not deterministic")
	}
}

func TestBuildHomeEmptyHistory(t *testing.T) {
	now := time.Now()
	var lib []Album
	for i := 0; i < 5; i++ {
		lib = append(lib, Album{UID: string(rune('a' + i)), AddedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	h := BuildHome(HomeInputs{Library: lib, Now: now})
	if len(h.Shelves) != 1 || h.Shelves[0].ID != ShelfRecentlyAdded || len(h.Shelves[0].Albums) != 5 {
		t.Fatalf("%+v", h.Shelves)
	}
}

func TestDaySeedStablePerDay(t *testing.T) {
	a := time.Date(2026, 1, 2, 1, 0, 0, 0, time.UTC)
	if DaySeed(a) != DaySeed(a.Add(20*time.Hour)) || DaySeed(a) == DaySeed(a.Add(24*time.Hour)) {
		t.Fatal("DaySeed not per-day")
	}
}
