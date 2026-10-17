package tagindex

import (
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestUnicodeRoundTrip is the regression for the review's §2.3: the old trie
// rebuilt keys with %c on a byte, so every non-ASCII tag came back mangled
// ("café" as "cafÃ©"), missed the caller's case map, and the corrupted string
// was what got written to the store.
func TestUnicodeRoundTrip(t *testing.T) {
	tags := []string{"café", "naïve", "über", "日本語", "Кириллица", "emoji🎄", "naïve-sub"}
	x := New()
	x.SetMaxResults(0)
	x.AddAll(tags)

	got := x.Search("")
	if len(got) != len(tags) {
		t.Fatalf("Search(\"\") returned %d tags, want %d: %q", len(got), len(tags), got)
	}
	want := append([]string(nil), tags...)
	sort.Strings(want)
	sort.Strings(got)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tag %d: got %q, want %q", i, got[i], want[i])
		}
	}

	// A prefix search must return the tag itself, byte for byte.
	for _, tag := range tags {
		res := x.Search(tag)
		found := false
		for _, r := range res {
			if r == tag {
				found = true
			}
		}
		if !found {
			t.Errorf("Search(%q) = %q, want it to contain the tag unchanged", tag, res)
		}
	}
}

func TestSearch(t *testing.T) {
	x := New()
	x.SetMaxResults(0)
	x.AddAll([]string{"Jazz", "jazz-fusion", "Blues", "java", "jazz"})

	// Case-insensitive prefix, sorted by lowercased key, original spelling kept
	// (the "jazz" key keeps the last spelling added).
	got := x.Search("jazz")
	want := []string{"jazz", "jazz-fusion"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Search(\"jazz\") = %q, want %q", got, want)
	}
	if got := x.Search("JAZZ"); len(got) != 2 {
		t.Errorf("Search is not case-insensitive on the query: %q", got)
	}
	if got := x.Search("nope"); got != nil {
		t.Errorf("Search(\"nope\") = %q, want none", got)
	}
	// A prefix that is a whole tag but not a prefix of any longer tag still
	// matches that tag.
	if got := x.Search("blues"); len(got) != 1 || got[0] != "Blues" {
		t.Errorf("Search(\"blues\") = %q, want [Blues]", got)
	}
}

// TestCaseVariantsMerge pins the documented behaviour: tags differing only by
// case collapse to one entry, keeping the last spelling added.
func TestCaseVariantsMerge(t *testing.T) {
	x := New()
	x.SetMaxResults(0)
	x.Add("Test")
	x.Add("TEST")
	if x.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (case variants merge)", x.Len())
	}
	got := x.Search("test")
	if len(got) != 1 || got[0] != "TEST" {
		t.Errorf("Search(\"test\") = %q, want [TEST] (last spelling wins)", got)
	}
}

func TestMaxResults(t *testing.T) {
	x := New()
	for i := 0; i < 500; i++ {
		x.Add(fmt.Sprintf("tag-%03d", i))
	}
	if got := len(x.Search("tag")); got != 100 {
		t.Errorf("default cap: got %d results, want 100", got)
	}
	x.SetMaxResults(0)
	if got := len(x.Search("tag")); got != 500 {
		t.Errorf("uncapped: got %d results, want 500", got)
	}
	x.SetMaxResults(7)
	if got := len(x.Search("tag")); got != 7 {
		t.Errorf("cap 7: got %d results, want 7", got)
	}
}

func TestResetAndHas(t *testing.T) {
	x := New()
	x.AddAll([]string{"a", "B"})
	if !x.Has("a") || !x.Has("b") || x.Has("c") {
		t.Fatal("Has is wrong")
	}
	x.Reset()
	if x.Len() != 0 || x.Has("a") {
		t.Fatal("Reset did not clear")
	}
	x.Add("later")
	if got := x.Search(""); len(got) != 1 || got[0] != "later" {
		t.Fatalf("index unusable after Reset: %q", got)
	}
}

// TestMemoryBudget pins the reason the trie was replaced: 20k tags cost
// 901 MB there. The slice index must stay in single-digit megabytes.
func TestMemoryBudget(t *testing.T) {
	const tags = 20000
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	x := New()
	x.SetMaxResults(0)
	for i := 0; i < tags; i++ {
		x.Add(fmt.Sprintf("artist-%d-album-%d-track", i*7919%100000, i*104729%100000))
	}
	x.Len() // force the build

	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(x)

	mb := float64(after.HeapAlloc-before.HeapAlloc) / 1e6
	t.Logf("%d tags -> %.1f MB", tags, mb)
	if mb > 20 {
		t.Errorf("%d tags cost %.1f MB, want under 20 MB", tags, mb)
	}
}

// TestMatchesNaiveFilter is a differential test: the index must agree with a
// plain case-insensitive HasPrefix filter over the same tags.
func TestMatchesNaiveFilter(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []rune("abcXYZ0123456789-_éü日")
	var tags []string
	for i := 0; i < 3000; i++ {
		n := 1 + rng.Intn(8)
		b := make([]rune, n)
		for j := range b {
			b[j] = alphabet[rng.Intn(len(alphabet))]
		}
		tags = append(tags, string(b))
	}
	x := New()
	x.SetMaxResults(0)
	x.AddAll(tags)

	naive := func(prefix string) []string {
		p := strings.ToLower(prefix)
		seen := map[string]bool{}
		var out []string
		for _, tag := range tags {
			if strings.HasPrefix(strings.ToLower(tag), p) {
				seen[strings.ToLower(tag)] = true
			}
		}
		for k := range seen {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}

	for i := 0; i < 300; i++ {
		p := string([]rune{alphabet[rng.Intn(len(alphabet))]})
		if rng.Intn(3) == 0 {
			p = strings.ToLower(p)
		}
		got := x.Search(p)
		want := naive(p)
		if len(got) != len(want) {
			t.Fatalf("prefix %q: index returned %d tags, naive filter %d", p, len(got), len(want))
		}
		keys := make([]string, len(got))
		for i, g := range got {
			keys[i] = strings.ToLower(g)
		}
		sort.Strings(keys)
		for i := range keys {
			if keys[i] != want[i] {
				t.Fatalf("prefix %q: got %q, want %q", p, keys, want)
			}
		}
	}
}

func BenchmarkSearch(b *testing.B) {
	x := New()
	x.SetMaxResults(0)
	for i := 0; i < 20000; i++ {
		x.Add(fmt.Sprintf("artist-%05d-track", i))
	}
	x.Len()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = x.Search("artist-01")
	}
}
