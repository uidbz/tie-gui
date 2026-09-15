package ui

import (
	"testing"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// Album grouping is what the compact playlist is for, so the run detection and
// the row↔playlist-index mapping are worth pinning down: an off-by-one there
// plays or removes the wrong track.
func TestBuildQueueRowsGroupsConsecutiveAlbums(t *testing.T) {
	tracks := []data.Track{
		{Hash: "a1", Title: "One", AlbumUID: "A", Album: "First", Artist: "X", Year: "1999"},
		{Hash: "a2", Title: "Two", AlbumUID: "A", Album: "First", Artist: "X"},
		{Hash: "b1", Title: "Three", AlbumUID: "B", Album: "Second", Artist: "Y"},
		{Hash: "a3", Title: "Four", AlbumUID: "A", Album: "First", Artist: "X"},
	}

	rows := buildQueueRows(tracks)

	// header A, 2 tracks, header B, 1 track, header A again, 1 track.
	if len(rows) != 7 {
		t.Fatalf("len(rows) = %d, want 7: %+v", len(rows), rows)
	}
	wantKinds := []queueRowKind{
		queueRowAlbum, queueRowTrack, queueRowTrack,
		queueRowAlbum, queueRowTrack,
		queueRowAlbum, queueRowTrack,
	}
	for i, want := range wantKinds {
		if rows[i].kind != want {
			t.Errorf("rows[%d].kind = %v, want %v", i, rows[i].kind, want)
		}
	}

	// Track rows must map to their backend playlist positions in order.
	var gotIdx []int
	for _, r := range rows {
		if r.kind == queueRowTrack {
			gotIdx = append(gotIdx, r.trackIndex)
		}
	}
	for i, idx := range gotIdx {
		if idx != i {
			t.Fatalf("track row %d has trackIndex %d, want %d (all: %v)", i, idx, i, gotIdx)
		}
	}

	// The re-appearance of album A is its own group: the queue is an ordered
	// play list, not a set of albums.
	if rows[0].groupStart != 0 || rows[0].groupCount != 2 {
		t.Errorf("first group = (%d,%d), want (0,2)", rows[0].groupStart, rows[0].groupCount)
	}
	if rows[3].groupStart != 2 || rows[3].groupCount != 1 {
		t.Errorf("second group = (%d,%d), want (2,1)", rows[3].groupStart, rows[3].groupCount)
	}
	if rows[5].groupStart != 3 || rows[5].groupCount != 1 {
		t.Errorf("third group = (%d,%d), want (3,1)", rows[5].groupStart, rows[5].groupCount)
	}
}

func TestBuildQueueRowsHeaderLabels(t *testing.T) {
	rows := buildQueueRows([]data.Track{
		{Title: "One", AlbumUID: "A", Album: "First", Artist: "X", Year: "1999"},
		{Title: "Two", AlbumUID: "A", Album: "First", Artist: "X", Year: "1999"},
	})
	if rows[0].title != "First" {
		t.Errorf("header title = %q, want %q", rows[0].title, "First")
	}
	if want := "X · 1999 · 2 tracks"; rows[0].subtitle != want {
		t.Errorf("header subtitle = %q, want %q", rows[0].subtitle, want)
	}

	// A single track must read "1 track", and a run whose artists disagree
	// drops the artist rather than picking one arbitrarily.
	rows = buildQueueRows([]data.Track{
		{Title: "Only", AlbumUID: "A", Album: "First", Artist: "X"},
		{Title: "Other", AlbumUID: "B", Album: "Second", Artist: "Y"},
	})
	if want := "X · 1 track"; rows[0].subtitle != want {
		t.Errorf("single-track subtitle = %q, want %q", rows[0].subtitle, want)
	}
}

// Tracks tie knows no album UID for still have to group, or a queue of loose
// files would render one header per row.
func TestBuildQueueRowsFallbackGrouping(t *testing.T) {
	rows := buildQueueRows([]data.Track{
		{Title: "One", Album: "Tagged"},
		{Title: "Two", Album: "Tagged"},
		{Filename: "loose1.mp3"},
		{Filename: "loose2.mp3"},
	})

	headers := 0
	for _, r := range rows {
		if r.kind == queueRowAlbum {
			headers++
		}
	}
	if headers != 2 {
		t.Fatalf("headers = %d, want 2 (one per album-tag run, one for the untagged run)", headers)
	}
	if rows[0].title != "Tagged" {
		t.Errorf("first header = %q, want %q", rows[0].title, "Tagged")
	}
	if rows[3].title != "Unknown album" {
		t.Errorf("second header = %q, want %q", rows[3].title, "Unknown album")
	}
}

func TestBuildQueueRowsEmpty(t *testing.T) {
	if rows := buildQueueRows(nil); len(rows) != 0 {
		t.Fatalf("buildQueueRows(nil) = %v, want empty", rows)
	}
}
