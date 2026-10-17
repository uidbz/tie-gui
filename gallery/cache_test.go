package gallery

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestShardedPathPinsCacheLayout guards an on-disk format. The sharding decides
// where every existing user's thumbnails live, so changing it silently orphans
// the whole cache: the next run misses everywhere and re-generates (and for tie
// entries, re-uploads) every thumbnail.
func TestShardedPathPinsCacheLayout(t *testing.T) {
	key := strings.Repeat("ab", 32) // a 64-hex content key
	got := shardedPath("/cache", key, "")
	want := filepath.Join("/cache", "ab", "ab", "ab", "ab", key)
	if got != want {
		t.Errorf("shardedPath = %s, want %s", got, want)
	}

	// The suffix keeps a directory preview and a plain thumbnail of the same
	// content apart.
	preview := shardedPath("/cache", key, "d")
	if preview == got {
		t.Error("a directory preview and a plain thumbnail collide on the same key")
	}
	if filepath.Base(preview) != key+"d" {
		t.Errorf("preview file = %q, want %q", filepath.Base(preview), key+"d")
	}

	// The directory of a thumbnail is where MkdirAll has to create the shard.
	if dir := filepath.Dir(got); dir != filepath.Join("/cache", "ab", "ab", "ab", "ab") {
		t.Errorf("shard dir = %s", dir)
	}
}

// TestCacheDirSpanMatchesKey pins the relationship the loop assumes: the span
// must be a multiple of the per-level character count, and must not run past a
// content hash.
func TestCacheDirSpanMatchesKey(t *testing.T) {
	if cacheDirSpan%cacheDirChars != 0 {
		t.Errorf("cacheDirSpan %d is not a multiple of cacheDirChars %d",
			cacheDirSpan, cacheDirChars)
	}
	if cacheDirSpan > 64 {
		t.Errorf("cacheDirSpan %d would read past a 64-char content hash", cacheDirSpan)
	}
}
