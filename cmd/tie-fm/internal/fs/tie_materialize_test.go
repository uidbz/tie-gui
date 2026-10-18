package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uidbz/tie-gui/tiestore"
)

func TestSafeName(t *testing.T) {
	cases := []struct {
		in, fallback, want string
	}{
		{"song.mp3", "hash", "song.mp3"},
		{"../.bashrc", "hash", ".bashrc"},
		{"../../etc/passwd", "hash", "passwd"},
		{"/etc/passwd", "hash", "passwd"},
		{"a/b/c.txt", "hash", "c.txt"},
		{".", "hash", "hash"},
		{"..", "hash", "hash"},
		{"", "hash", "hash"},
		{"\x00\x00", "hash", "hash"},
		{"weird\x00name.txt", "hash", "weirdname.txt"},
	}
	for _, c := range cases {
		if got := SafeName(c.in, c.fallback); got != c.want {
			t.Errorf("SafeName(%q, %q) = %q, want %q", c.in, c.fallback, got, c.want)
		}
	}
}

// TieFS.Materialize is now a thin pass-through to tiestore.Cache, whose own
// tests (tiestore/tiestore_test.go) cover atomicity, the size check and the
// untrusted-name case. What is worth pinning here is the pass-through itself:
// the entry's hash, size and name reach the cache, and the cache's errors reach
// the caller.
func TestTieFSMaterializePassesTheEntryThrough(t *testing.T) {
	const hash = "0000000000000000000000000000000000000000000000000000000000000001"
	const full = "complete contents"

	var gotHash, gotDest string
	var attempts int
	tfs := &TieFS{cache: tiestore.NewCache("tie-fm-test-", func(h, dest string) error {
		attempts++
		gotHash, gotDest = h, dest
		if attempts == 1 {
			// An interrupted transfer writes bytes and fails; the partial file
			// must never be handed to a later call.
			if err := os.WriteFile(dest, []byte("com"), 0o600); err != nil {
				return err
			}
			return errors.New("connection reset")
		}
		return os.WriteFile(dest, []byte(full), 0o600)
	})}
	defer tfs.Close()

	e := Entry{Name: "song.mp3", Hash: hash, Size: int64(len(full))}
	if _, err := tfs.Materialize(e); err == nil {
		t.Fatal("a truncated download was accepted")
	}

	path, err := tfs.Materialize(e)
	if err != nil {
		t.Fatalf("retry after a truncated download: %v", err)
	}
	if gotHash != hash {
		t.Errorf("downloader saw hash %q, want %q", gotHash, hash)
	}
	if filepath.Base(gotDest) == "song.mp3" || strings.Contains(gotDest, "song.mp3") {
		t.Errorf("the store-supplied name reached the download path: %q", gotDest)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != full {
		t.Errorf("materialized content = %q, %v; want %q", got, err, full)
	}
}

// A store-supplied name that is a path escape must not move the file outside
// the cache. The mechanism lives in tiestore; this is the tie-fm-side guard.
func TestTieFSMaterializeIgnoresStoreSuppliedName(t *testing.T) {
	const hash = "0000000000000000000000000000000000000000000000000000000000000002"
	root := t.TempDir()
	tfs := &TieFS{cache: tiestore.NewCache("tie-fm-test-", func(h, dest string) error {
		return os.WriteFile(dest, []byte("payload"), 0o600)
	})}
	defer tfs.Close()

	for _, name := range []string{"../../escape", "/etc/passwd", "..", "a/../../b"} {
		path, err := tfs.Materialize(Entry{Name: name, Hash: hash, Size: 7})
		if err != nil {
			t.Fatalf("Materialize(name=%q): %v", name, err)
		}
		dir := filepath.Dir(path)
		if strings.Contains(path, "escape") || strings.Contains(path, "passwd") {
			t.Errorf("store-supplied name leaked into the path: %q", path)
		}
		if rel, err := filepath.Rel(root, dir); err == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("the cache dir %q is inside the test's own tree", dir)
		}
	}
	// Nothing was written in the test's tree.
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		t.Errorf("files written outside the cache dir: %v", entries)
	}
}

func TestTieFSMaterializeRejectsUnusableHash(t *testing.T) {
	tfs := &TieFS{cache: tiestore.NewCache("tie-fm-test-", func(h, dest string) error { return nil })}
	defer tfs.Close()
	if _, err := tfs.Materialize(Entry{Name: "x", Hash: "../../etc/passwd"}); err == nil {
		t.Error("a non-hash content address was accepted")
	}
	if _, err := tfs.Materialize(Entry{Name: "x"}); !errors.Is(err, tiestore.ErrNoHash) {
		t.Errorf("no content hash: err = %v, want tiestore.ErrNoHash", err)
	}
}

func TestTieFSCloseRemovesCache(t *testing.T) {
	tfs := &TieFS{cache: tiestore.NewCache("tie-fm-test-", func(h, dest string) error {
		return os.WriteFile(dest, []byte("x"), 0o600)
	})}
	path, err := tfs.Materialize(Entry{Name: "f", Hash: strings.Repeat("a", 64), Size: 1})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := tfs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cache dir survived Close: %v", err)
	}
	if err := tfs.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
