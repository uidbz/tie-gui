package main

import (
	"reflect"
	"testing"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
)

func TestMoveBookmark(t *testing.T) {
	mk := func(labels ...string) []config.Bookmark {
		out := make([]config.Bookmark, len(labels))
		for i, l := range labels {
			out[i] = config.Bookmark{Label: l}
		}
		return out
	}
	labels := func(b []config.Bookmark) []string {
		out := make([]string, len(b))
		for i, x := range b {
			out[i] = x.Label
		}
		return out
	}

	cases := []struct {
		name string
		from []string
		argF int
		argT int
		want []string
	}{
		{"move down one", []string{"A", "B", "C", "D"}, 1, 2, []string{"A", "C", "B", "D"}},
		{"move down to end", []string{"A", "B", "C", "D"}, 0, 3, []string{"B", "C", "D", "A"}},
		{"move up one", []string{"A", "B", "C", "D"}, 2, 1, []string{"A", "C", "B", "D"}},
		{"move up to start", []string{"A", "B", "C", "D"}, 3, 0, []string{"D", "A", "B", "C"}},
		{"noop same", []string{"A", "B", "C"}, 1, 1, []string{"A", "B", "C"}},
	}
	for _, c := range cases {
		got := moveBookmark(mk(c.from...), c.argF, c.argT)
		if !reflect.DeepEqual(labels(got), c.want) {
			t.Errorf("%s: got %v, want %v", c.name, labels(got), c.want)
		}
	}
}