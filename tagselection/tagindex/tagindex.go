// Package tagindex is the prefix-searchable tag set behind tagselection.
//
// It replaces a 256-way byte trie. That trie allocated a 256-pointer node per
// character — 20 000 realistic tag names measured 385 786 nodes and 901 MB of
// heap, about 45 KB per tag — and it rebuilt each result key on the way out
// with fmt.Sprintf("%s%c", prefix, c) where c is a byte, so every byte ≥ 0x80
// was re-encoded as its Latin-1 code point: "café" came back as "cafÃ©" and
// "日本語" as mojibake. The mangled key then missed the caller's case map, so
// the corrupted string was what the dropdown showed and what selecting it wrote
// into the store.
//
// A sorted slice with a binary search is ~1 MB for the same 20 000 tags,
// searches in O(log n + k), answers in a stable order that is at least
// case-consistent, and never reconstructs a key from anything — the stored
// string is the answer.
package tagindex

import (
	"sort"
	"strings"
)

// Index holds a set of tags and answers prefix searches against their
// lowercased form. It is not safe for concurrent use; the owning widget builds
// one, reads it from the UI goroutine, and swaps a freshly built one in.
type Index struct {
	entries []entry // sorted by key once built
	built   []entry // insertion buffer before the first search
	dirty   bool
	max     int // result cap for Search; 0 = unlimited
}

type entry struct {
	display string // what the caller added, and what Search returns
	key     string // lowercased display, the sort and match key
}

// New returns an empty index. Search is capped at 100 results by default;
// see SetMaxResults.
func New() *Index {
	return &Index{max: 100}
}

// SetMaxResults caps the number of results Search returns. 0 means unlimited.
func (x *Index) SetMaxResults(n int) { x.max = n }

// Add inserts a tag. Insertion is O(1): the index stays unsorted until the
// first search, so loading a whole collection's tags costs one sort instead of
// one splice per tag.
func (x *Index) Add(tag string) {
	x.built = append(x.built, entry{display: tag, key: strings.ToLower(tag)})
	x.dirty = true
}

// AddAll adds many tags at once.
func (x *Index) AddAll(tags []string) {
	for _, tag := range tags {
		x.built = append(x.built, entry{display: tag, key: strings.ToLower(tag)})
	}
	x.dirty = true
}

// Reset drops every tag, keeping the configured result cap.
func (x *Index) Reset() {
	x.entries = x.entries[:0]
	x.built = x.built[:0]
	x.dirty = false
}

// Len reports how many distinct tags the index holds.
func (x *Index) Len() int {
	x.build()
	return len(x.entries)
}

// Has reports whether tag is in the index, comparing case-insensitively.
func (x *Index) Has(tag string) bool {
	x.build()
	key := strings.ToLower(tag)
	i := x.search(key)
	return i < len(x.entries) && x.entries[i].key == key
}

// Search returns up to max tags whose lowercased form starts with prefix,
// compared case-insensitively. An empty prefix returns the whole index (up to
// the cap). Results keep the spelling they were added with.
func (x *Index) Search(prefix string) []string {
	x.build()
	prefix = strings.ToLower(prefix)
	start := x.search(prefix)
	if start >= len(x.entries) || !strings.HasPrefix(x.entries[start].key, prefix) {
		return nil
	}
	out := make([]string, 0, 16)
	for i := start; i < len(x.entries); i++ {
		if !strings.HasPrefix(x.entries[i].key, prefix) {
			break
		}
		if x.max > 0 && len(out) >= x.max {
			break
		}
		out = append(out, x.entries[i].display)
	}
	return out
}

// search returns the index of the first entry whose key is not less than key.
func (x *Index) search(key string) int {
	return sort.Search(len(x.entries), func(i int) bool { return x.entries[i].key >= key })
}

// build sorts and dedupes the insertion buffer. Tags differing only by case
// collapse to one entry, keeping the last spelling added — the behaviour the
// old case map documented.
func (x *Index) build() {
	if !x.dirty {
		return
	}
	x.dirty = false
	if len(x.built) == 0 {
		x.entries = x.entries[:0]
		return
	}
	all := append(x.entries[:0:0], x.built...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].key < all[j].key })
	out := make([]entry, 0, len(all))
	for i := 0; i < len(all); i++ {
		// Keep the last of each equal run, so the newest spelling wins.
		if i+1 < len(all) && all[i+1].key == all[i].key {
			continue
		}
		out = append(out, all[i])
	}
	x.entries = out
	x.built = x.built[:0]
}
