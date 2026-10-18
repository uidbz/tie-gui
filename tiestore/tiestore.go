// Package tiestore owns the store plumbing the apps used to each re-implement:
// building an HTTP client for a filehost, fetching a blob, and materializing
// one on disk. Every one of those steps had a bug in at least one copy — a
// truncated file served from the cache forever, a download path built from an
// untrusted filename, a request with no deadline, a session temp directory
// that was never removed — and fixing them meant fixing them three times.
//
// It is deliberately small. The tie client (github.com/uidbz/tie/client) owns
// queries, triples and the config; this package only covers the parts that
// were duplicated on top of it. The gallery.Thumbnailer and fs.FileSystem
// interfaces are unchanged: this is what they call, not a replacement for them.
package tiestore

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/io/getlib"

	"github.com/uidbz/tie-gui/internal/httpx"
)

// ErrNoHash is returned when an entry carries no usable content hash.
var ErrNoHash = errors.New("tiestore: entry has no content hash")

// Client returns the pooled, deadline-bounded HTTP client for a bulk transfer
// (a whole blob). Short fetches — thumbnails, covers, stat probes — want
// httpx.Small instead.
func Client(host client.FileHost) *http.Client { return httpx.Bulk(host) }

// Fetch reads a whole blob into memory and hands back a seekable reader, which
// is what the gallery's thumbnail path and the archive readers want. getlib
// verifies the content hash, so the bytes are either what the store says they
// are or the call fails.
func Fetch(host client.FileHost, hash string) (io.ReadSeeker, error) {
	if hash == "" {
		return nil, ErrNoHash
	}
	return getlib.ReadBytes(Client(host), host.URL, hash)
}

// FetchSmall reads a small blob — a thumbnail or a cover — on the short
// deadline, so a half-dead filehost fails the tile quickly instead of parking
// a loader worker.
func FetchSmall(host client.FileHost, hash string) (io.ReadSeeker, error) {
	if hash == "" {
		return nil, ErrNoHash
	}
	return getlib.ReadBytes(httpx.Small(host), host.URL, hash)
}

// Downloader fetches hash into dest, a file path that does not exist yet.
//
// The contract is that a failure leaves nothing at dest. The cache publishes an
// entry by the file's existence, so a partial file sitting at the final path
// poisons the cache for the rest of the session. Production passes
// (*client.TieClient).Download("", hash, dest), which honours the contract since
// tie v0.7.2: getlib.DownloadFile stages each file in a sibling temp file and
// renames it into place only once the bytes hash to the address they were
// fetched under, removing the staged file on any failure.
//
// Materialize also removes dest on its error paths, so a downloader that breaks
// the contract costs one wasted re-download rather than a corrupt cache.
type Downloader func(hash, dest string) error

// NewDownloader returns the production downloader for a tie client and
// (optionally) a named filehost.
func NewDownloader(tc *client.TieClient, hostName string) Downloader {
	return func(hash, dest string) error { return tc.Download(hostName, hash, dest) }
}

// Cache is one session's materialization cache: a private directory holding
// complete blobs keyed by content hash, removed when the session ends.
//
// Three things it exists to get right, each of which was once wrong:
//
//   - A cached file being what it claims to be. A cancelled export or a dropped
//     connection used to leave a truncated file at the cache path, and the
//     "already downloaded" Stat hit meant every later call handed out the
//     corrupt file with no way back. The transfer half of that is fixed
//     upstream: since tie v0.7.2 the download itself stages and renames (see
//     Downloader). What the cache adds is the other half — the length is
//     checked against the size the store recorded before the file is accepted,
//     and anything rejected is removed instead of left there to be served next
//     time.
//   - Untrusted names. The cache path used to be hash + "-" + name, where name
//     is the store's filename triple copied verbatim out of the database.
//     Nothing constrains it to a basename, so "../../.bashrc" escaped the
//     cache directory. Keying on the content hash alone removes the untrusted
//     component; name is kept only for error messages.
//   - Lifetime. The directory used to be created lazily without
//     synchronisation while downloads ran on two goroutines, and never
//     removed, so every preview and export left its bytes on disk.
type Cache struct {
	prefix   string
	download Downloader

	mu  sync.Mutex
	dir string
}

// NewCache returns a cache whose blobs are fetched with download. prefix is
// the os.TempDir prefix of the directory it creates, so the app's startup
// sweep (internal/appboot) recognises it if the process is killed.
func NewCache(prefix string, download Downloader) *Cache {
	return &Cache{prefix: prefix, download: download}
}

// Dir returns the cache directory, creating it on first use.
func (c *Cache) Dir() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirLocked()
}

func (c *Cache) dirLocked() (string, error) {
	if c.dir == "" {
		d, err := os.MkdirTemp("", c.prefix)
		if err != nil {
			return "", err
		}
		c.dir = d
	}
	return c.dir, nil
}

// Materialize returns a path inside the cache holding hash's bytes, downloading
// them on demand and keeping the result for the rest of the session.
// expectedSize is the size the store recorded; when it is > 0 a download of a
// different length is rejected rather than cached. name is used only to make
// the error readable and never reaches the path.
func (c *Cache) Materialize(hash string, expectedSize int64, name string) (string, error) {
	if hash == "" {
		return "", fmt.Errorf("%w: %s", ErrNoHash, name)
	}
	if !isHexHash(hash) {
		return "", fmt.Errorf("tiestore: unusable content hash for %s: %q", name, hash)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	dir, err := c.dirLocked()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, hash)
	if info, err := os.Stat(dest); err == nil && (expectedSize <= 0 || info.Size() == expectedSize) {
		return dest, nil // already downloaded, and the size still matches
	}

	// The downloader is responsible for making dest appear complete or not at
	// all; the removals here are the belt to that braces, because a stale
	// partial file is exactly the failure this cache exists to prevent.
	if err := c.download(hash, dest); err != nil {
		os.Remove(dest)
		return "", err
	}
	info, err := os.Stat(dest)
	if err != nil {
		os.Remove(dest)
		return "", err
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		os.Remove(dest)
		return "", fmt.Errorf("tiestore: download of %s is %d bytes, expected %d",
			display(name, hash), info.Size(), expectedSize)
	}
	return dest, nil
}

// Close removes the cache. Materialized files exist only to feed a transfer or
// an external program, so nothing outlives the session that made them. Safe to
// call more than once.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir == "" {
		return nil
	}
	err := os.RemoveAll(c.dir)
	c.dir = ""
	return err
}

// display names an entry in an error message without letting store-controlled
// text look like a path.
func display(name, hash string) string {
	if name == "" {
		return hash[:12]
	}
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(name)
}

// isHexHash is the same 64-hex check tie's metadata package applies; kept
// local so a cache key can never contain a path separator.
func isHexHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// FetchWithTimeout is the escape hatch for a caller that needs a deadline
// other than the two defaults — a multi-gigabyte archive export, say.
func FetchWithTimeout(host client.FileHost, hash string, timeout time.Duration) (io.ReadSeeker, error) {
	if hash == "" {
		return nil, ErrNoHash
	}
	return getlib.ReadBytes(httpx.WithTimeout(host, timeout), host.URL, hash)
}
