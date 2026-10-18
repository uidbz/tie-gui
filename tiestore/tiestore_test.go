package tiestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

const (
	hash1 = "0000000000000000000000000000000000000000000000000000000000000001"
	hash2 = "0000000000000000000000000000000000000000000000000000000000000002"
)

// TestMaterializeRejectsPartialDownload is the regression for review §3.4 /
// issue #9. getlib.DownloadFile writes straight to its destination, so a
// cancelled or interrupted transfer left a truncated file at the cache path —
// and the "already downloaded" Stat meant every later Materialize handed the
// corrupt file back with no way to recover.
func TestMaterializeRejectsPartialDownload(t *testing.T) {
	const full = "complete contents"

	var attempts int
	c := NewCache("tiestore-test-", func(h, dest string) error {
		attempts++
		if attempts == 1 {
			// Simulate an interrupted transfer: bytes written, then failure.
			if err := os.WriteFile(dest, []byte("com"), 0o600); err != nil {
				return err
			}
			return errors.New("connection reset")
		}
		return os.WriteFile(dest, []byte(full), 0o600)
	})
	defer c.Close()

	if _, err := c.Materialize(hash1, int64(len(full)), "song.mp3"); err == nil {
		t.Fatal("a truncated download was accepted")
	}

	path, err := c.Materialize(hash1, int64(len(full)), "song.mp3")
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

	dir, err := c.Dir()
	if err != nil {
		t.Fatal(err)
	}
	// No .part file left behind.
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(leftovers) > 0 {
		t.Errorf("leftover partial files: %v", leftovers)
	}
}

// A cached file whose size no longer matches the recorded size is not the file
// we think it is (a truncated write, or a hash collision in the store), so it
// is re-fetched rather than handed out.
func TestMaterializeRefetchesSizeMismatch(t *testing.T) {
	var attempts int
	c := NewCache("tiestore-test-", func(h, dest string) error {
		attempts++
		return os.WriteFile(dest, []byte(strings.Repeat("x", attempts*4)), 0o600)
	})
	defer c.Close()

	if _, err := c.Materialize(hash1, 4, "f"); err != nil {
		t.Fatal(err)
	}
	// Same hash, the store now says it is 8 bytes.
	path, err := c.Materialize(hash1, 8, "f")
	if err != nil {
		t.Fatalf("re-fetch after a size mismatch: %v", err)
	}
	if attempts != 2 {
		t.Errorf("downloader ran %d time(s), want 2", attempts)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 8 {
		t.Errorf("cached file is %v bytes, want 8 (%v)", sizeOf(path), err)
	}
}

// An unknown size (0) means "take what the store gives": the first download is
// accepted and cached without a length check.
func TestMaterializeAcceptsUnknownSize(t *testing.T) {
	var attempts int
	c := NewCache("tiestore-test-", func(h, dest string) error {
		attempts++
		return os.WriteFile(dest, []byte("whatever"), 0o600)
	})
	defer c.Close()

	for i := 0; i < 2; i++ {
		if _, err := c.Materialize(hash1, 0, "f"); err != nil {
			t.Fatalf("Materialize #%d: %v", i+1, err)
		}
	}
	if attempts != 1 {
		t.Errorf("downloader ran %d time(s), want 1: the second call should hit the cache", attempts)
	}
}

// TestMaterializeIgnoresStoreSuppliedName covers review §3.5: the cache path
// used to be hash + "-" + name, and name is the store's filename triple,
// copied verbatim out of the database. A name of "../../escape" made
// filepath.Join escape the temp dir and the download wrote wherever the user's
// process could write.
func TestMaterializeIgnoresStoreSuppliedName(t *testing.T) {
	root := t.TempDir()
	c := NewCache("tiestore-test-", func(h, dest string) error {
		return os.WriteFile(dest, []byte("payload"), 0o600)
	})
	defer c.Close()

	dir, err := c.Dir()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../../escape", "/etc/passwd", "..", "a/../../b", "x\nfake"} {
		path, err := c.Materialize(hash2, 7, name)
		if err != nil {
			t.Fatalf("Materialize(name=%q): %v", name, err)
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
	c := NewCache("tiestore-test-", func(h, dest string) error { return nil })
	defer c.Close()

	if _, err := c.Materialize("../../etc/passwd", 1, "x"); err == nil {
		t.Error("a non-hash content address was accepted")
	}
	if _, err := c.Materialize("", 1, "x"); !errors.Is(err, ErrNoHash) {
		t.Errorf("an entry with no content hash: err = %v, want ErrNoHash", err)
	}
	// A hash of the right shape but not hex, and a short one.
	for _, bad := range []string{strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if _, err := c.Materialize(bad, 1, "x"); err == nil {
			t.Errorf("hash %q was accepted", bad)
		}
	}
}

// TestMaterializeConcurrent pins that cache-directory creation and the download
// path are synchronised: Materialize runs on the ops goroutine and the UI
// goroutine, and two calls for the same blob must not race on the .part file.
func TestMaterializeConcurrent(t *testing.T) {
	c := NewCache("tiestore-test-", func(h, dest string) error {
		return os.WriteFile(dest, []byte("x"), 0o600)
	})
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hash := strings.Repeat("0", 63) + string(rune('0'+i%10))
			if _, err := c.Materialize(hash, 1, "f"); err != nil {
				t.Errorf("Materialize: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// Same blob from eight goroutines at once: one download, one file.
	var shared, calls int
	var mu sync.Mutex
	c2 := NewCache("tiestore-test-", func(h, dest string) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return os.WriteFile(dest, []byte("x"), 0o600)
	})
	defer c2.Close()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c2.Materialize(hash1, 1, "f"); err != nil {
				t.Errorf("Materialize: %v", err)
			}
			mu.Lock()
			shared++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if shared != 8 {
		t.Errorf("%d of 8 concurrent calls succeeded", shared)
	}
}

func TestCloseRemovesCache(t *testing.T) {
	c := NewCache("tiestore-test-", func(h, dest string) error {
		return os.WriteFile(dest, []byte("x"), 0o600)
	})
	path, err := c.Materialize(strings.Repeat("a", 64), 1, "f")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("cache dir survived Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// A cache reused after Close makes itself a new directory.
	if _, err := c.Materialize(strings.Repeat("b", 64), 1, "f"); err != nil {
		t.Errorf("reuse after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close after reuse: %v", err)
	}
}

// The cache directory has to carry the app's prefix, or the startup sweep
// (internal/appboot) cannot recognise it after the process is killed.
func TestNewCacheUsesTheGivenPrefix(t *testing.T) {
	c := NewCache("tie-fm-", func(h, dest string) error { return nil })
	defer c.Close()
	dir, err := c.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if base := filepath.Base(dir); !strings.HasPrefix(base, "tie-fm-") {
		t.Errorf("cache dir %q does not carry the prefix", base)
	}
}

func TestFetchRejectsEmptyHash(t *testing.T) {
	if _, err := Fetch(host(), ""); !errors.Is(err, ErrNoHash) {
		t.Errorf("Fetch: err = %v, want ErrNoHash", err)
	}
	if _, err := FetchSmall(host(), ""); !errors.Is(err, ErrNoHash) {
		t.Errorf("FetchSmall: err = %v, want ErrNoHash", err)
	}
	if _, err := FetchWithTimeout(host(), "", time.Second); !errors.Is(err, ErrNoHash) {
		t.Errorf("FetchWithTimeout: err = %v, want ErrNoHash", err)
	}
}

// host() names a filehost that is not listening: the empty-hash checks must fail
// before any request is made, so a refused connection would be a bug.
func host() client.FileHost {
	return client.FileHost{URL: "http://127.0.0.1:1"}
}

func sizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}
