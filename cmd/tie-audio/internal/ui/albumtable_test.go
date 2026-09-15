package ui

import (
	"reflect"
	"testing"
)

func columnKeys(cols []albumColumn) []string {
	keys := make([]string, 0, len(cols))
	for _, c := range cols {
		keys = append(keys, c.key)
	}
	return keys
}

// The queue and the album view have different defaults — artwork earns a column
// in a queue of mixed albums but repeats one cover in a single album's track
// list — so the fallback set is per-view.
func TestResolveAlbumColumnsFallbackIsPerView(t *testing.T) {
	album := columnKeys(resolveAlbumColumns(nil, defaultAlbumColumns))
	if want := []string{"trackno", "title", "artist", "album", "year", "duration"}; !reflect.DeepEqual(album, want) {
		t.Errorf("album default = %v, want %v", album, want)
	}

	queue := columnKeys(resolveAlbumColumns(nil, allAlbumColumns))
	if len(queue) == 0 || queue[0] != "cover" {
		t.Errorf("queue default = %v, want the cover column first", queue)
	}

	compact := columnKeys(resolveAlbumColumns(nil, compactAlbumColumns))
	if want := []string{"trackno", "title", "duration"}; !reflect.DeepEqual(compact, want) {
		t.Errorf("compact default = %v, want %v", compact, want)
	}
}

func TestResolveAlbumColumnsKeepsOrderAndDropsJunk(t *testing.T) {
	got := columnKeys(resolveAlbumColumns([]string{"duration", "cover", "nope", "duration", "title"}, defaultAlbumColumns))
	if want := []string{"duration", "cover", "title"}; !reflect.DeepEqual(got, want) {
		t.Errorf("resolved = %v, want %v (unknown and duplicate keys dropped, order kept)", got, want)
	}

	// A config listing only stale keys must not produce an empty table.
	if got := resolveAlbumColumns([]string{"gone", "also-gone"}, compactAlbumColumns); len(got) != len(compactAlbumColumns) {
		t.Errorf("stale-only config resolved to %v, want the fallback set", columnKeys(got))
	}
}

// The artwork column must be narrow and fixed: it is an image, and giving it
// the default text-column width would push the title off a narrow pane.
func TestCoverColumnWidth(t *testing.T) {
	if w := columnFixedWidth("cover"); w >= columnFixedWidth("title") || w <= 0 {
		t.Errorf("cover column width = %v, want a small positive value", w)
	}
}
