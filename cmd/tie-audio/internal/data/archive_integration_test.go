package data

import (
	"archive/zip"
	"bytes"
	"image"
	_ "image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uidbz/tie/client"
)

// testEnvConfig mirrors the tie repo's test-env (triplestore :2161, filehost
// :2162, namespace Collections, collection Main). DefaultCollection is what
// NewTieClient binds (Collection alone is only the fallback name), so it must
// be set for the client to operate on Main.
func testEnvConfig() client.Config {
	cfg := client.DefaultConfig()
	cfg.Namespace = "Collections"
	cfg.Collection = "Main"
	cfg.DefaultCollection = "Main"
	cfg.TripleStoreURL = "http://localhost:2161"
	cfg.DefaultFileHosts = []string{"default"}
	cfg.FileHosts = map[string]client.FileHost{"default": {URL: "http://localhost:2162"}}
	return cfg
}

// requireTestEnv skips unless the tie test-env is running locally.
func requireTestEnv(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, "http://localhost:2162/"+strings.Repeat("0", 64), nil)
	if err != nil {
		t.Skip(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skipf("tie test-env not running (start it with ../tie/test-env/start.sh): %v", err)
	}
	resp.Body.Close()
}

// TestArchiveTracksIntegration runs the archive flow against the real tie
// test-env with real tagged FLAC members: the archive is uploaded, resolved
// to tracks (tags parsed), members are re-fetched through the same URL shape
// pwplay will use, and the extracted cover lands on the archive's thumbnail
// relation.
func TestArchiveTracksIntegration(t *testing.T) {
	requireTestEnv(t)

	// Zip the fixture album (three tagged FLACs + a cover image).
	entries, err := os.ReadDir("testdata/archive-src")
	if err != nil {
		t.Skip("archive fixtures not present:", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata/archive-src", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := testEnvConfig()
	host := cfg.FileHosts["default"]
	zipPath := filepath.Join(t.TempDir(), "album.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := client.UploadTo(host, zipPath)
	if err != nil || res.ErrorMsg != "" {
		t.Fatalf("uploading archive: %v %s", err, res.ErrorMsg)
	}
	zipHash := res.Items[0].Hash

	s := &Session{Tie: client.NewTieClient(cfg)}
	album := Album{UID: zipHash, Kind: AlbumArchive, Title: "Test Archive Album"}
	tracks, err := s.AlbumTracks(album)
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("got %d tracks, want 3: %+v", len(tracks), tracks)
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].TrackNo < tracks[j].TrackNo })
	for i, tr := range tracks {
		n := strconv.Itoa(i + 1)
		if tr.Title != "Track "+n {
			t.Errorf("tracks[%d].Title = %q, want %q", i, tr.Title, "Track "+n)
		}
		if tr.Artist != "Test Artist" {
			t.Errorf("tracks[%d].Artist = %q, want Test Artist", i, tr.Artist)
		}
		if tr.Album != "Test Archive Album" {
			t.Errorf("tracks[%d].Album = %q, want Test Archive Album", i, tr.Album)
		}
		if tr.TrackNo != i+1 {
			t.Errorf("tracks[%d].TrackNo = %d, want %d", i, tr.TrackNo, i+1)
		}
		if tr.Year != "2024" {
			t.Errorf("tracks[%d].Year = %q, want 2024", i, tr.Year)
		}
		if tr.Duration < 0.2 || tr.Duration > 1 {
			t.Errorf("tracks[%d].Duration = %v, want ~0.3s", i, tr.Duration)
		}
		if tr.AlbumUID != zipHash {
			t.Errorf("tracks[%d].AlbumUID = %q, want the archive hash", i, tr.AlbumUID)
		}
		// The URL handed to pwplay must serve the member bytes.
		resp, err := http.Get(s.StreamURL(tr.Hash))
		if err != nil {
			t.Fatalf("streaming %s: %v", tr.Filename, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("streaming %s: status %s, %v", tr.Filename, resp.Status, err)
		}
		want, err := os.ReadFile(filepath.Join("testdata/archive-src", tr.Filename))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, want) {
			t.Errorf("streamed %s does not match the archived member", tr.Filename)
		}
	}

	// The cover.jpg member becomes the archive's thumbnail relation.
	var thumbHash string
	for i := 0; i < 20; i++ { // the triplestore may take a beat to sync
		row, err := s.Tie.Get(zipHash)
		if err == nil {
			thumbHash = client.RowFirst(row, "thumbnail")
		}
		if thumbHash != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if thumbHash == "" {
		t.Fatal("no thumbnail relation recorded on the archive")
	}
	resp, err := http.Get(s.StreamURL(thumbHash))
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("cover thumbnail does not decode: %v", err)
	}
	if w := img.Bounds().Dx(); w > archiveCoverEdge {
		t.Errorf("cover thumbnail width = %d, want <= %d", w, archiveCoverEdge)
	}
}
