package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
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

// TestMaterializeRejectsPartialDownload is the regression for review §3.4. The
// download library writes straight to its destination, so a cancelled or
// interrupted transfer left a truncated file at the cache path - and the
// "already downloaded" Stat meant every later Materialize handed the corrupt
// file back with no way to recover.
func TestMaterializeRejectsPartialDownload(t *testing.T) {
	const hash = "0000000000000000000000000000000000000000000000000000000000000001"
	const full = "complete contents"

	tfs := &TieFS{}
	var attempts int
	tfs.download = func(h, dest string) error {
		attempts++
		if attempts == 1 {
			// Simulate an interrupted transfer: bytes written, then failure.
			if err := os.WriteFile(dest, []byte("com"), 0600); err != nil {
				return err
			}
			return errors.New("connection reset")
		}
		return os.WriteFile(dest, []byte(full), 0600)
	}

	e := Entry{Name: "song.mp3", Hash: hash, Size: int64(len(full))}
	if _, err := tfs.Materialize(e); err == nil {
		t.Fatal("a truncated download was accepted")
	}

	path, err := tfs.Materialize(e)
	if err != nil {
		t.Fatalf("retry after a truncated download: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("the corrupt file was reused: downloader ran %d time(s)", attempts)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != full {
		t.Errorf("materialized content = %q, want %q", got, full)
	}
	// No .part file left behind.
	if leftovers, _ := filepath.Glob(filepath.Join(tfs.tmpDir, "*.part")); len(leftovers) > 0 {
		t.Errorf("leftover partial files: %v", leftovers)
	}
}

// TestMaterializeIgnoresStoreSuppliedName covers review §3.5: the cache path
// used to be hash + "-" + e.Name, and e.Name is the store's filename triple.
// A name of "../../escape" made filepath.Join escape the temp dir and the
// download wrote wherever the user's process could write.
func TestMaterializeIgnoresStoreSuppliedName(t *testing.T) {
	const hash = "0000000000000000000000000000000000000000000000000000000000000002"
	root := t.TempDir()

	tfs := &TieFS{download: func(h, dest string) error {
		return os.WriteFile(dest, []byte("payload"), 0600)
	}}
	defer tfs.Close()

	for _, name := range []string{"../../escape", "/etc/passwd", "..", "a/../../b"} {
		path, err := tfs.Materialize(Entry{Name: name, Hash: hash, Size: 7})
		if err != nil {
			t.Fatalf("Materialize(name=%q): %v", name, err)
		}
		dir, err := tfs.cacheDir()
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("name %q escaped the cache dir: path=%q cache=%q", name, path, dir)
		}
		if strings.Contains(path, "escape") || strings.Contains(path, "passwd") {
			t.Errorf("store-supplied name leaked into the path: %q", path)
		}
	}
	// Nothing was written outside the cache.
	if entries, err := os.ReadDir(root); err == nil && len(entries) > 0 {
		t.Errorf("files written outside the cache dir: %v", entries)
	}
}

func TestMaterializeRejectsUnusableHash(t *testing.T) {
	tfs := &TieFS{download: func(h, dest string) error { return nil }}
	defer tfs.Close()
	if _, err := tfs.Materialize(Entry{Name: "x", Hash: "../../etc/passwd"}); err == nil {
		t.Error("a non-hash content address was accepted")
	}
	if _, err := tfs.Materialize(Entry{Name: "x"}); err == nil {
		t.Error("an entry with no content hash was accepted")
	}
}

// TestMaterializeConcurrent pins that tmpDir creation is synchronised:
// Materialize runs on the ops goroutine and the UI goroutine.
func TestMaterializeConcurrent(t *testing.T) {
	tfs := &TieFS{download: func(h, dest string) error {
		return os.WriteFile(dest, []byte("x"), 0600)
	}}
	defer tfs.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hash := strings.Repeat("0", 63) + string(rune('0'+i%10))
			if _, err := tfs.Materialize(Entry{Name: "f", Hash: hash, Size: 1}); err != nil {
				t.Errorf("Materialize: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestTieFSCloseRemovesCache(t *testing.T) {
	tfs := &TieFS{download: func(h, dest string) error {
		return os.WriteFile(dest, []byte("x"), 0600)
	}}
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
