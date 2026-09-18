package ui

import (
	"image"
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// newTestWallTable builds a wall table with a stub cover store (no server) and
// a stub album opener; the table's hooks are exercised through the stubs.
func newTestWallTable(t *testing.T) (*wallTable, *browsePage) {
	t.Helper()
	test.NewApp()
	covers := newCoverStore(nil)
	covers.decodeFn = func(string) (image.Image, error) { return nil, data.ErrNoCover }
	page := &browsePage{
		win:     test.NewWindow(nil),
		session: &data.Session{Cfg: config.AppConfig{}},
		covers:  covers,
	}
	return newWallTable(page), page
}

func wallAlbums() []data.Album {
	return []data.Album{
		{UID: "u1", Kind: data.AlbumDir, Title: "Bridges", Artist: "Cult", Year: "2001"},
		{UID: "u2", Kind: data.AlbumArchive, Title: "arrival", Artist: "OST", Year: "2016"},
		{UID: "u3", Kind: data.AlbumTrack, Title: "One More Time", Artist: "Daft Punk", Year: "2000"},
	}
}

// The wall table's default and compact sets differ from the track table's:
// albums have no track number or duration, and the compact set keeps only what
// a wall tile would show (cover, title, artist).
func TestResolveWallColumnsFallbackIsPerLayout(t *testing.T) {
	regular := columnKeys(resolveAlbumColumns(nil, defaultWallColumns, allWallColumns))
	if want := []string{"cover", "title", "artist", "year", "kind"}; !reflect.DeepEqual(regular, want) {
		t.Errorf("regular default = %v, want %v", regular, want)
	}

	compact := columnKeys(resolveAlbumColumns(nil, compactWallColumns, allWallColumns))
	if want := []string{"cover", "title", "artist"}; !reflect.DeepEqual(compact, want) {
		t.Errorf("compact default = %v, want %v", compact, want)
	}

	// Unknown and duplicate keys drop out; a stale-only config falls back.
	got := columnKeys(resolveAlbumColumns([]string{"kind", "nope", "kind", "title"}, defaultWallColumns, allWallColumns))
	if want := []string{"kind", "title"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resolved = %v, want %v", got, want)
	}
}

func TestWallTableCellValue(t *testing.T) {
	wt, _ := newTestWallTable(t)
	a := data.Album{UID: "u1", Kind: data.AlbumDir, Title: "Bridges", Artist: "Cult", Year: "2001"}
	cases := map[string]string{
		"cover":  "u1", // the Art column carries the album UID for the cover cell
		"title":  "Bridges",
		"artist": "Cult",
		"year":   "2001",
		"kind":   "Album",
	}
	for key, want := range cases {
		if got := wt.cellValue(key, a); got != want {
			t.Errorf("cellValue(%q) = %q, want %q", key, got, want)
		}
	}

	// A title-less album falls back to its UID's base name (Album.Display).
	untitled := data.Album{UID: "u2", Kind: data.AlbumTrack}
	if got := wt.cellValue("title", untitled); got != "u2" {
		t.Errorf("untitled title = %q, want the UID fallback", got)
	}

	kinds := map[data.AlbumKind]string{
		data.AlbumDir:     "Album",
		data.AlbumArchive: "Archive",
		data.AlbumTrack:   "Track",
	}
	for kind, want := range kinds {
		if got := albumKindLabel(kind); got != want {
			t.Errorf("albumKindLabel(%v) = %q, want %q", kind, got, want)
		}
	}
}

func TestCompareAlbums(t *testing.T) {
	albums := wallAlbums()
	// Title sort is case-insensitive: "arrival" sorts before "Bridges".
	if compareAlbums("title", albums[1], albums[0]) >= 0 {
		t.Error("title: arrival should sort before Bridges (case-insensitive)")
	}
	if compareAlbums("artist", albums[0], albums[2]) >= 0 {
		t.Error("artist: Cult should sort before Daft Punk")
	}
	if compareAlbums("year", albums[2], albums[0]) >= 0 {
		t.Error("year: 2000 should sort before 2001")
	}
	if compareAlbums("kind", albums[0], albums[1]) >= 0 {
		t.Error("kind: Album should sort before Archive")
	}
}

// albumAt translates a page-relative display row through the table's paging
// offset, so activation hits the right album past page one.
func TestWallTableAlbumAtMapsPageOffset(t *testing.T) {
	wt, _ := newTestWallTable(t)
	wt.albums = wallAlbums()

	a, ok := wt.albumAt(1)
	if !ok || a.UID != "u2" {
		t.Errorf("albumAt(1) = %q/%v, want u2", a.UID, ok)
	}
	wt.table.Offset = 1
	a, ok = wt.albumAt(1)
	if !ok || a.UID != "u3" {
		t.Errorf("albumAt(1) with offset 1 = %q/%v, want u3", a.UID, ok)
	}
	wt.table.Offset = 2
	if _, ok = wt.albumAt(1); ok {
		// offset 2 + row 1 = index 3, past the end
		t.Error("albumAt past the end should report !ok")
	}
}

// A chosen sort is re-applied when a fresh listing arrives, so a wall reload
// keeps the display order instead of snapping back to feed order.
func TestWallTableSetAlbumsReappliesSort(t *testing.T) {
	wt, _ := newTestWallTable(t)
	wt.setAlbums(wallAlbums())
	wt.onSort("Title", true) // ascending: arrival, Bridges, One More Time

	if wt.albums[0].Title != "arrival" || wt.albums[1].Title != "Bridges" {
		t.Fatalf("after sort: order = %q, %q, %q", wt.albums[0].Title, wt.albums[1].Title, wt.albums[2].Title)
	}

	wt.setAlbums([]data.Album{
		{UID: "u4", Kind: data.AlbumDir, Title: "ZZ Top"},
		{UID: "u5", Kind: data.AlbumDir, Title: "abba"},
	})
	if wt.albums[0].Title != "abba" || wt.albums[1].Title != "ZZ Top" {
		t.Errorf("setAlbums did not re-apply the sort: %q, %q", wt.albums[0].Title, wt.albums[1].Title)
	}
}

// Row activation opens the album at the displayed (sorted) position, and the
// table's Data/RowCount feed the displayed TableData from the album slice.
func TestWallTableActivationAndData(t *testing.T) {
	wt, _ := newTestWallTable(t)
	var opened []string
	wt.open = func(a data.Album) { opened = append(opened, a.UID) }
	wt.setAlbums(wallAlbums())

	if n := wt.table.RowCount(); n != 3 {
		t.Errorf("RowCount = %d, want 3", n)
	}
	td := wt.table.Data(0, 1000)
	if td.RowCount() != 3 {
		t.Fatalf("Data rows = %d, want 3", td.RowCount())
	}
	// Column order follows the default set; the Art column holds the UID.
	if got := td.Get(0, 0); got != "u1" {
		t.Errorf("Art cell = %q, want u1", got)
	}
	if got := td.Get(1, 0); got != "Bridges" {
		t.Errorf("Title cell = %q, want Bridges", got)
	}

	wt.onSort("Title", false) // descending: One More Time, Bridges, arrival
	wt.table.OnRowActivated(0)
	if len(opened) != 1 || opened[0] != "u3" {
		t.Errorf("activated row 0 after desc sort opened %v, want [u3]", opened)
	}
}

// The table lays out in a real window and renders its cells from the displayed
// data (the cover cell reads the album UID out of the Art column's cell text).
func TestWallTableLayoutInWindow(t *testing.T) {
	wt, _ := newTestWallTable(t)
	wt.setAlbums(wallAlbums())

	win := test.NewWindow(wt.object)
	win.Resize(fyne.NewSize(900, 500))
	wt.show()
	win.Canvas().Refresh(wt.object)

	ft := wt.table.GetFlexTable()
	if got := ft.CellText(1, 2); got != "One More Time" {
		t.Errorf("displayed title row 2 = %q, want One More Time", got)
	}
	if got := ft.CellText(4, 1); got != "Archive" {
		t.Errorf("displayed kind row 1 = %q, want Archive", got)
	}
}
