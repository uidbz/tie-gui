package gallery

import (
	"reflect"
	"testing"
)

func TestPaginationSlotCount(t *testing.T) {
	tests := []struct {
		width float32
		want  int
	}{
		{0, minPageSlots},
		{paginationReservedWidth, minPageSlots},
		{paginationReservedWidth + pageSlotWidth, minPageSlots},
		{paginationReservedWidth + 3*pageSlotWidth, 3},
		{1024, 5},
		{1920, maxPageSlots},
		{100000, maxPageSlots},
	}
	for _, tt := range tests {
		if got := paginationSlotCount(tt.width); got != tt.want {
			t.Errorf("paginationSlotCount(%v) = %d, want %d", tt.width, got, tt.want)
		}
	}
}

func TestPageSlots(t *testing.T) {
	tests := []struct {
		current, maxPages, slots int
		want                     []int
	}{
		// Fewer pages than slots: show all.
		{0, 1, 6, []int{0}},
		{0, 3, 6, []int{0, 1, 2}},
		{2, 3, 6, []int{0, 1, 2}},
		// Exactly slots pages: still no elision.
		{0, 4, 4, []int{0, 1, 2, 3}},
		// More pages than slots: first/last pinned, budget spent around the
		// current page; hidden ranges collapse to middle "…" gaps.
		{0, 10, 4, []int{0, 1, 2, 9}},       // 1 2 3 … 10
		{5, 10, 4, []int{0, 4, 5, 9}},       // 1 … 5 6 … 10
		{9, 10, 4, []int{0, 7, 8, 9}},       // 1 … 8 9 10
		{8, 10, 4, []int{0, 7, 8, 9}},       // 1 … 8 9 10
		{3, 10, 6, []int{0, 1, 2, 3, 4, 9}}, // 1 2 3 4 5 … 10
		{5, 10, 6, []int{0, 3, 4, 5, 6, 9}}, // 1 … 4 5 6 7 … 10
		{0, 10, 6, []int{0, 1, 2, 3, 4, 9}}, // 1 2 3 4 5 … 10
		{4, 10, 3, []int{0, 4, 9}},          // 1 … 5 … 10
		{0, 10, 2, []int{0, 9}},             // 1 … 10
		{9, 10, 2, []int{0, 9}},             // 1 … 10
	}
	for _, tt := range tests {
		if got := pageSlots(tt.current, tt.maxPages, tt.slots); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("pageSlots(%d, %d, %d) = %v, want %v",
				tt.current, tt.maxPages, tt.slots, got, tt.want)
		}
	}
}
