package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// groupedQueuePage builds a queue page whose playlist holds two albums of two
// tracks each, so the grouped table's row model is
// [H(A), T(a1), T(a2), H(B), T(b1), T(b2)].
func groupedQueuePage(t *testing.T) *queuePage {
	t.Helper()
	test.NewApp()
	backend := &scriptBackend{}
	q := newTestQueuePage(backend)
	urls := []string{"a1", "a2", "b1", "b2"}
	metas := []data.Track{
		{Hash: "a1", Title: "A One", Artist: "Artist A", Album: "Album A", AlbumUID: "A", TrackNo: 1},
		{Hash: "a2", Title: "A Two", Artist: "Artist A", Album: "Album A", AlbumUID: "A", TrackNo: 2},
		{Hash: "b1", Title: "B One", Artist: "Artist B", Album: "Album B", AlbumUID: "B", TrackNo: 1},
		{Hash: "b2", Title: "B Two", Artist: "Artist B", Album: "Album B", AlbumUID: "B", TrackNo: 2},
	}
	q.transport.SetQueue(urls, metas)
	q.playlist = append([]string{}, urls...)
	q.rebuildTracks()
	return q
}

func TestGroupedTableBuildsHeaderRows(t *testing.T) {
	q := groupedQueuePage(t)
	rows := q.table.rows
	if len(rows) != 6 {
		t.Fatalf("grouped rows = %d, want 6 (2 headers + 4 tracks)", len(rows))
	}
	for _, i := range []int{0, 3} {
		if rows[i].kind != queueRowAlbum {
			t.Errorf("row %d kind = %v, want an album header", i, rows[i].kind)
		}
	}
	if rows[0].title != "Album A" || rows[3].title != "Album B" {
		t.Errorf("header titles = %q/%q, want Album A/Album B", rows[0].title, rows[3].title)
	}
	if rows[0].albumUID != "A" || rows[3].albumUID != "B" {
		t.Errorf("header album UIDs = %q/%q, want A/B", rows[0].albumUID, rows[3].albumUID)
	}
}

func TestGroupedTableDisplayToPlaylistMapping(t *testing.T) {
	q := groupedQueuePage(t)

	cases := []struct{ row, want int }{
		{0, -1}, // header A
		{1, 0},  // a1
		{2, 1},  // a2
		{3, -1}, // header B
		{4, 2},  // b1
		{5, 3},  // b2
	}
	for _, c := range cases {
		if got := q.playlistIndexForRow(c.row); got != c.want {
			t.Errorf("playlistIndexForRow(%d) = %d, want %d", c.row, got, c.want)
		}
	}

	// Lenient mapping: a header maps to its first track (drop = before the album).
	if got := q.playlistIndexForRowLenient(3); got != 2 {
		t.Errorf("playlistIndexForRowLenient(3) = %d, want 2 (Album B's first track)", got)
	}

	// Display gaps → playlist gaps.
	gapCases := []struct{ gap, want int }{
		{0, 0}, // before header A = before a1
		{1, 0}, // between header A and a1 = before a1
		{3, 2}, // before header B = before b1
		{6, 4}, // past the end
	}
	for _, c := range gapCases {
		if got := q.playlistGapAt(c.gap); got != c.want {
			t.Errorf("playlistGapAt(%d) = %d, want %d", c.gap, got, c.want)
		}
	}
}

// The play indicator marks the display row whose track is current, wherever
// the grouping moved it.
func TestGroupedTableRowIndicatorFollowsCurrent(t *testing.T) {
	q := groupedQueuePage(t)
	q.current = 2 // b1, display row 4
	if got := q.rowIndicator(4); got == "" {
		t.Error("rowIndicator(4) empty, want the play glyph for the current track")
	}
	if got := q.rowIndicator(1); got != "" {
		t.Errorf("rowIndicator(1) = %q, want empty for a non-current track", got)
	}
	if got := q.rowIndicator(0); got != "" {
		t.Errorf("rowIndicator(0) = %q, want empty for a header row", got)
	}
}

// Header rows are inert: no selection, no drag, no double-tap play.
func TestGroupedTableHeadersAreInert(t *testing.T) {
	q := groupedQueuePage(t)
	ft := q.table.table.GetFlexTable()
	if ft.RowSelectable == nil {
		t.Fatal("grouped table has no RowSelectable guard")
	}
	if ft.RowSelectable(0) {
		t.Error("header row is selectable, want inert")
	}
	if !ft.RowSelectable(1) {
		t.Error("track row is not selectable")
	}

	backend := q.session.Backend.(*scriptBackend)
	q.playDisplayRow(0) // header: must not play
	q.playDisplayRow(4) // b1: plays playlist index 2
	waitForCond(t, "goto for the double-tapped track", func() bool { return backend.gotoCount() == 1 })
	if backend.gotos[0] != 2 {
		t.Errorf("goto = %d, want 2 (header ignored, track played)", backend.gotos[0])
	}
}

// A single-row drag maps display rows to playlist indices through the grouped
// model: dragging a1 (display 1, playlist 0) onto b1 (display 4, playlist 2)
// moves the track two playlist slots.
func TestGroupedTableReorderMapsDisplayRows(t *testing.T) {
	q := groupedQueuePage(t)
	q.onReorder(1, 4)
	want := []string{"a2", "b1", "a1", "b2"}
	if len(q.playlist) != len(want) {
		t.Fatalf("playlist = %v, want %v", q.playlist, want)
	}
	for i := range want {
		if q.playlist[i] != want[i] {
			t.Fatalf("playlist = %v, want %v", q.playlist, want)
		}
	}
}

// The title column's cell renders an album header or a track title depending
// on the row kind.
func TestQueueTitleCellRendersBothKinds(t *testing.T) {
	q := groupedQueuePage(t)
	titleCol := 0
	for i, c := range q.table.cols {
		if c.key == "title" {
			titleCol = q.table.colOffset() + i
		}
	}

	cell := newQueueTitleCell(nil)
	q.table.updateGroupedCell(titleCol, 0, cell) // header row
	if cell.albumTitle.Text != "Album A" {
		t.Errorf("header cell title = %q, want Album A", cell.albumTitle.Text)
	}
	if cell.albumDetail.Text == "" {
		t.Error("header cell detail empty, want the artist·year·count line")
	}

	q.table.updateGroupedCell(titleCol, 1, cell) // track row
	if cell.trackLabel.Text != "A One" {
		t.Errorf("track cell label = %q, want A One", cell.trackLabel.Text)
	}
}

// Laying out the grouped table in a real (test-driver) window must not panic:
// the mixed header/track row heights and the per-kind cells are exercised end
// to end.
func TestGroupedTableLaysOut(t *testing.T) {
	q := groupedQueuePage(t)
	win := test.NewWindow(q.object)
	win.Resize(fyne.NewSize(900, 500))
	q.table.show()
	win.Canvas().Refresh(q.object)
}
