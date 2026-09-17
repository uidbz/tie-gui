package data

import (
	"bytes"
	"errors"
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
	if err := s.Tie.ImportFile(dst, cfg.FileHosts["default"], cfg.Collection, nil, uid, client.TieUnknownFile); err != nil {
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
	embedded, err := os.ReadFile("testdata/archive-src/cover.jpg")
	if err != nil {
		t.Skip("fixture not present:", err)
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
	if !bytes.Equal(got, embedded) {
		t.Errorf("embedded fallback = %d bytes, want the %d embedded bytes", len(got), len(embedded))
	}

	got, err = s.CoverBytesForUID(string(externalUID))
	if err != nil {
		t.Fatalf("CoverBytesForUID(external dir): %v", err)
	}
	if !bytes.Equal(got, external) {
		t.Errorf("external image = %d bytes, want the %d external bytes", len(got), len(external))
	}

	if _, err := s.CoverBytesForUID(string(noneUID)); !errors.Is(err, ErrNoCover) {
		t.Errorf("coverless album = %v, want ErrNoCover", err)
	}

	got, err = s.CoverBytesForUID(trackHash)
	if err != nil {
		t.Fatalf("CoverBytesForUID(standalone track): %v", err)
	}
	if !bytes.Equal(got, embedded) {
		t.Errorf("standalone track cover = %d bytes, want the %d embedded bytes", len(got), len(embedded))
	}

	// A recorded thumbnail relation wins over the embedded picture.
	if err := s.Tie.Set(string(embeddedUID), "thumbnail", []string{externalHash}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tie.Sync(); err != nil {
		t.Fatal(err)
	}
	got, err = s.CoverBytesForUID(string(embeddedUID))
	if err != nil {
		t.Fatalf("CoverBytesForUID(thumbnail relation): %v", err)
	}
	if !bytes.Equal(got, external) {
		t.Errorf("thumbnail relation = %d bytes, want the %d thumbnail bytes", len(got), len(external))
	}
}
