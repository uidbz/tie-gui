package ui

import (
	"testing"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// The selects speak labels; the data layer speaks values. Every label must
// round-trip, or changing scope/mode in the UI silently searches the wrong way.
func TestSearchLabelRoundTrips(t *testing.T) {
	for _, label := range searchKindLabels {
		if got := searchKindLabel(searchKindByLabel(label)); got != label {
			t.Errorf("kind label %q round-trips to %q", label, got)
		}
	}
	for _, label := range searchModeLabels {
		if got := searchModeLabel(searchModeByLabel(label)); got != label {
			t.Errorf("mode label %q round-trips to %q", label, got)
		}
	}
	if searchModeLabel("") != "Words" {
		t.Error("empty mode should display as the profile default (Words)")
	}
}

func TestSearchOptionsSnapshot(t *testing.T) {
	b := &browsePage{
		searchMode:   client.SearchFuzzy,
		searchKind:   data.SearchTracksOnly,
		searchFields: []client.Relation{client.RelTitle},
	}
	opts := b.searchOptions()
	if opts.Mode != client.SearchFuzzy || opts.Scope != data.SearchTracksOnly || len(opts.Fields) != 1 {
		t.Errorf("options = %+v, want the control state", opts)
	}

	// Zero state = profile defaults (words, everything, full vocabulary).
	empty := (&browsePage{}).searchOptions()
	if empty.Mode != "" || empty.Scope != data.SearchAll || empty.Fields != nil {
		t.Errorf("zero options = %+v, want profile defaults", empty)
	}
}

// A control change must be inert unless a search is actually on screen —
// rerunSearch runs from the scope/mode selects' callbacks on every change.
func TestRerunSearchInertWithoutActiveQuery(t *testing.T) {
	b := &browsePage{}
	b.rerunSearch() // feed != feedSearch: must not touch feeds or fetch
	b.feed = feedSearch
	b.searchQuery = ""
	b.rerunSearch() // empty query: still inert
	if b.feed != feedSearch {
		t.Errorf("feed = %v, untouched", b.feed)
	}
}
