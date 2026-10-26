package ui

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/internal/tietest"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
)

// TestApplyReleaseToAlbumIntegration exercises the real tie write path against
// the tie test-env (triplestore :2161 / filehost :2162). Start it with
// `(cd /go/src/github.com/uidbz/tie/test-env && ./start.sh)` and run:
//
//	TIE_FM_TIE_TEST=1 go test ./cmd/tie-fm/internal/ui -run TestApplyReleaseToAlbumIntegration -v
func TestApplyReleaseToAlbumIntegration(t *testing.T) {
	if os.Getenv("TIE_FM_TIE_TEST") == "" {
		t.Skip("set TIE_FM_TIE_TEST=1 and start the tie test-env")
	}
	tc := client.NewTieClient(tietest.Config())
	if _, err := tc.ServerVersion(); err != nil {
		t.Skipf("tie test-env not reachable: %v", err)
	}
	host, err := tc.ResolveHost("")
	if err != nil {
		t.Fatal(err)
	}

	dirPath := "/tie-fm-mb-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	uid, err := tc.MkTieDirAll(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "album")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	titles := []string{"first", "second"}
	var hashes []string
	for i, title := range titles {
		wav := filepath.Join(dir, fmt.Sprintf("%02d.wav", i+1))
		writeTestWAV(t, wav, byte(i+1))
		hash, err := tc.WriteFile(host, uid, fmt.Sprintf("%02d.wav", i+1), wav, nil)
		if err != nil {
			t.Fatal(err)
		}
		b := tc.NewBatch()
		b.Set(hash, "title", []string{title})
		b.Set(hash, "track", []string{strconv.Itoa(i + 1)})
		b.Set(hash, "disc", []string{"1"})
		b.Set(hash, "tie-type", []string{"audio-file"})
		if _, err := tc.Batch(b); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
	}
	if err := tc.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := client.SetDirTypes(tc, uid, []string{"audio-dir"}); err != nil {
		t.Fatal(err)
	}

	rel := &musicbrainz.Release{
		MBID:             "rel-test-mbid",
		Title:            "Test Album",
		Artist:           musicbrainz.Artist{MBID: "artist-test-mbid", Name: "Test Artist"},
		ArtistCredit:     "Test Artist",
		ReleaseGroupMBID: "rg-test-mbid",
		Year:             1999,
		TrackCount:       2,
		DiscCount:        1,
		Genres:           []string{"rock"},
		Mediums: []musicbrainz.Medium{{
			Position: 1, TrackCount: 2,
			Tracks: []musicbrainz.Track{
				{MBID: "track-1", RecordingMBID: "rec-1", Title: "Come Together", ArtistCredit: "Test Artist", Position: 1, Disc: 1},
				{MBID: "track-2", RecordingMBID: "rec-2", Title: "Something", ArtistCredit: "Test Artist", Position: 2, Disc: 1},
			},
		}},
	}

	if err := applyReleaseToAlbum(tc, client.FileURIScheme+dirPath, rel, false, config.DefaultMusicBrainz()); err != nil {
		t.Fatal(err)
	}

	row, err := tc.Get(string(uid))
	if err != nil {
		t.Fatal(err)
	}
	for relName, want := range map[string]string{
		"album":                  "Test Album",
		"artist":                 "Test Artist",
		"album-artist":           "Test Artist",
		"year":                   "1999",
		"musicbrainz-release-id": "rel-test-mbid",
	} {
		if got := client.RowFirst(row, client.Relation(relName)); got != want {
			t.Errorf("dir %s = %q, want %q", relName, got, want)
		}
	}
	tags, err := client.GetTags(tc, string(uid))
	if err != nil {
		t.Fatal(err)
	}
	foundGenre := false
	for _, tag := range tags {
		if tag == "rock" {
			foundGenre = true
		}
	}
	if !foundGenre {
		t.Errorf("genre tag missing: %v", tags)
	}

	rows, err := tc.Expand(hashes)
	if err != nil {
		t.Fatal(err)
	}
	byHash := map[string]client.Row{}
	for _, r := range rows {
		byHash[r.Key] = r
	}
	for i, want := range []struct{ title, rec string }{
		{"Come Together", "rec-1"},
		{"Something", "rec-2"},
	} {
		r, ok := byHash[hashes[i]]
		if !ok {
			t.Fatalf("missing row for %s", hashes[i])
		}
		if got := client.RowFirst(r, "title"); got != want.title {
			t.Errorf("track %d title = %q, want %q", i+1, got, want.title)
		}
		if got := client.RowFirst(r, "musicbrainz-recording-id"); got != want.rec {
			t.Errorf("track %d recording id = %q, want %q", i+1, got, want.rec)
		}
		if got := client.RowFirst(r, "album"); got != "Test Album" {
			t.Errorf("track %d album = %q", i+1, got)
		}
	}
}

// writeTestWAV writes a tiny valid 8kHz mono PCM WAV so tie classifies the
// uploaded blob as audio.
func writeTestWAV(t *testing.T, path string, fill byte) {
	t.Helper()
	const samples = 800
	data := make([]byte, samples)
	for i := range data {
		data[i] = fill
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	write := func(v any) {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	f.WriteString("RIFF")
	write(uint32(36 + len(data)))
	f.WriteString("WAVEfmt ")
	write(uint32(16))   // PCM chunk size
	write(uint16(1))    // PCM
	write(uint16(1))    // mono
	write(uint32(8000)) // sample rate
	write(uint32(8000)) // byte rate
	write(uint16(1))    // block align
	write(uint16(8))    // bits per sample
	f.WriteString("data")
	write(uint32(len(data)))
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
}

// TestAddMusicBrainzTagsOnlyIntegration verifies the tag-bar action writes only
// tags: existing tags survive, genres/community tags are unioned, and no album
// metadata or MBIDs are touched.
func TestAddMusicBrainzTagsOnlyIntegration(t *testing.T) {
	if os.Getenv("TIE_FM_TIE_TEST") == "" {
		t.Skip("set TIE_FM_TIE_TEST=1 and start the tie test-env")
	}
	tc := client.NewTieClient(tietest.Config())
	if _, err := tc.ServerVersion(); err != nil {
		t.Skipf("tie test-env not reachable: %v", err)
	}
	dirPath := "/tie-fm-mb-tags-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	uid, err := tc.MkTieDirAll(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetTags(tc, string(uid), []string{"keepme"}); err != nil {
		t.Fatal(err)
	}
	b := tc.NewBatch()
	b.Set(string(uid), "album", []string{"Old Album"})
	if _, err := tc.Batch(b); err != nil {
		t.Fatal(err)
	}
	if err := tc.Sync(); err != nil {
		t.Fatal(err)
	}

	rel := &musicbrainz.Release{
		MBID:        "rel-tags-only",
		Title:       "New Album",
		Genres:      []string{"rock", "Rock"},
		Tags:        []string{"pop", "keepme"},
		HasCoverArt: false,
	}
	added, err := addReleaseTagsOnly(tc, uid, rel)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 2 || added[0] != "rock" || added[1] != "pop" {
		t.Errorf("added = %v, want [rock pop]", added)
	}
	tags, err := client.GetTags(tc, string(uid))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tag := range tags {
		got[tag] = true
	}
	for _, want := range []string{"keepme", "rock", "pop"} {
		if !got[want] {
			t.Errorf("missing tag %q in %v", want, tags)
		}
	}
	if len(got) != 3 {
		t.Errorf("tags = %v, want exactly keepme/rock/pop", tags)
	}

	row, err := tc.Get(string(uid))
	if err != nil {
		t.Fatal(err)
	}
	if got := client.RowFirst(row, "album"); got != "Old Album" {
		t.Errorf("album metadata was rewritten: %q", got)
	}
	if got := client.RowFirst(row, "musicbrainz-release-id"); got != "" {
		t.Errorf("tags-only wrote a release MBID: %q", got)
	}

	// Re-running is a no-op for tags.
	if added, err := addReleaseTagsOnly(tc, uid, rel); err != nil || len(added) != 0 {
		t.Errorf("second run added %v, err %v", added, err)
	}
}
