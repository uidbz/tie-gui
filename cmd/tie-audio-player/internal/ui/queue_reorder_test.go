package ui

import (
	"reflect"
	"testing"
)

func TestReorderSelectionGap(t *testing.T) {
	cases := []struct {
		name string
		n    int
		sel  []int
		gap  int
		want []int // nil ⇒ no-op
	}{
		{"block down to end", 5, []int{1, 2}, 5, []int{0, 3, 4, 1, 2}},
		{"block down mid", 5, []int{1, 2}, 4, []int{0, 3, 1, 2, 4}},
		{"block up to top", 5, []int{3, 4}, 0, []int{3, 4, 0, 1, 2}},
		{"noop just above", 5, []int{1, 2}, 1, nil},
		{"noop just below", 5, []int{1, 2}, 3, nil},
		{"noninteg block down", 6, []int{0, 2}, 5, []int{1, 3, 4, 0, 2, 5}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reorderSelection(c.n, c.sel, c.gap)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("reorderSelection(%d,%v,%d) = %v, want %v", c.n, c.sel, c.gap, got, c.want)
			}
		})
	}
}

// reorderMoves must produce a move sequence that turns the identity order into
// the target permutation when applied left-to-right via moveOneInt.
func TestReorderMovesReproducesPermutation(t *testing.T) {
	perms := [][]int{
		{0, 3, 4, 1, 2},
		{3, 4, 0, 1, 2},
		{1, 3, 4, 0, 2, 5},
		{2, 0, 1},
	}
	for _, want := range perms {
		n := len(want)
		work := make([]int, n)
		for i := range work {
			work[i] = i
		}
		for _, m := range reorderMoves(want) {
			moveOneInt(work, m[0], m[1])
		}
		if !reflect.DeepEqual(work, want) {
			t.Fatalf("applying reorderMoves gave %v, want %v", work, want)
		}
	}
}
