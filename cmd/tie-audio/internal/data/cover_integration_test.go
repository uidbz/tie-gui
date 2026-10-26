package data

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

// importCoverFile writes data to a temp file under name and imports it into
// the tie directory uid, returning its content hash.
func importCoverFile(t *testing.T, s *Session, cfg client.Config, uid client.DirUID, data []byte, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Tie.ImportFile(dst, cfg.FileHosts["default"], nil, uid, client.TieUnknownFile); err != nil {
		t.Fatalf("importing %s: %v", name, err)
	}
	return hashOf(t, data)
}

func importCoverFixture(t *testing.T, s *Session, cfg client.Config, uid client.DirUID, src, name string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skip("fixture not present:", err)
	}
	return importCoverFile(t, s, cfg, uid, data, name)
}

// TestCoverBytesEmbeddedFallback runs the full cover resolution against the
// tie test-env: an external image beats an embedded picture, the first
// track's embedded picture is the fallback when no image file exists, a
// standalone track probes its own blob, and a recorded thumbnail relation
// wins over everything.
func TestCoverBytesEmbeddedFallback(t *testing.T) {
	requireTestEnv(t)
	cfg := testEnvConfig()
	s := &Session{Tie: client.NewTieClient(cfg)}
	base := "tie:/cover-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	mkDir := func(name string) client.DirUID {
		t.Helper()
		uid, err := s.Tie.MkTieDirAll(base + "/" + name)
		if err != nil {
			t.Fatalf("MkTieDirAll %s: %v", name, err)
		}
		return uid
	}
	external := tinyJPEG(t) // different bytes than the embedded picture

	// No external image: the first track (filename order) is probed and its
	// embedded picture wins; the second track has none. Distinct fixture
	// content per role: a filename is one value on a shared content hash, so
	// reusing content under another name would make the order arbitrary.
	embeddedUID := mkDir("embedded")
	importCoverFixture(t, s, cfg, embeddedUID, "testdata/archive-src/03 - Track 3.flac", "02 - Plain.flac")
	importCoverFixture(t, s, cfg, embeddedUID, "testdata/track-with-cover.flac", "01 - With Cover.flac")

	// An external image file beats the embedded picture.
	externalUID := mkDir("external")
	importCoverFixture(t, s, cfg, externalUID, "testdata/track-with-cover.flac", "01 - With Cover.flac")
	externalHash := importCoverFile(t, s, cfg, externalUID, external, "cover.jpg")

	// Neither image nor embedded picture: settled coverless.
	noneUID := mkDir("none")
	importCoverFixture(t, s, cfg, noneUID, "testdata/archive-src/02 - Track 2.flac", "01 - Plain.flac")

	// A standalone track probes its own blob.
	standaloneUID := mkDir("standalone")
	trackHash := importCoverFixture(t, s, cfg, standaloneUID, "testdata/track-with-cover.flac", "01 - Standalone.flac")

	// Batch writes are visible to queries only after a sync.
	if err := s.Tie.Sync(); err != nil {
		t.Fatal(err)
	}

	got, err := s.CoverBytesForUID(string(embeddedUID))
	if err != nil {
		t.Fatalf("CoverBytesForUID(embedded dir): %v", err)
	}
	assertCoverThumbnail(t, got)

	// The resolved cover is cached on the filehost and mapped on the album,
	// so the next launch (and every other view) fetches the small thumbnail
	// instead of re-deriving it from the track.
	row, err := s.Tie.Get(string(embeddedUID))
	if err != nil {
		t.Fatalf("Get(embedded dir): %v", err)
	}
	if th := client.RowFirst(row, "thumbnail"); th == "" {
		t.Error("embedded dir has no thumbnail relation after resolution")
	}
	again, err := s.CoverBytesForUID(string(embeddedUID))
	if err != nil {
		t.Fatalf("second CoverBytesForUID(embedded dir): %v", err)
	}
	assertCoverThumbnail(t, again)
	if !bytes.Equal(got, again) {
		t.Errorf("cached cover = %d bytes, want the %d bytes generated first", len(again), len(got))
	}

	got, err = s.CoverBytesForUID(string(externalUID))
	if err != nil {
		t.Fatalf("CoverBytesForUID(external dir): %v", err)
	}
	assertCoverThumbnail(t, got)

	if _, err := s.CoverBytesForUID(string(noneUID)); !errors.Is(err, ErrNoCover) {
		t.Errorf("coverless album = %v, want ErrNoCover", err)
	}

	got, err = s.CoverBytesForUID(trackHash)
	if err != nil {
		t.Fatalf("CoverBytesForUID(standalone track): %v", err)
	}
	assertCoverThumbnail(t, got)

	// A recorded thumbnail relation wins over the embedded picture, and is
	// returned as-is (it is already a thumbnail-sized blob). Use a fresh
	// session: the first one has the previously generated thumbnail cached as
	// a listing hint.
	if err := s.Tie.Set(string(embeddedUID), "thumbnail", []string{externalHash}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tie.Sync(); err != nil {
		t.Fatal(err)
	}
	fresh := &Session{Tie: client.NewTieClient(cfg)}
	got, err = fresh.CoverBytesForUID(string(embeddedUID))
	if err != nil {
		t.Fatalf("CoverBytesForUID(thumbnail relation): %v", err)
	}
	if !bytes.Equal(got, external) {
		t.Errorf("thumbnail relation = %d bytes, want the %d thumbnail bytes", len(got), len(external))
	}
}

// assertCoverThumbnail checks that got decodes as an image no larger than the
// cover thumbnail edge — the shape CoverBytesForUID now returns for a cover it
// had to derive from a full-size source.
func assertCoverThumbnail(t *testing.T, got []byte) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("cover bytes do not decode: %v", err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w > coverThumbEdge || h > coverThumbEdge {
		t.Errorf("cover thumbnail is %dx%d, want both edges <= %d", w, h, coverThumbEdge)
	}
	if len(got) == 0 {
		t.Error("cover thumbnail is empty")
	}
}
