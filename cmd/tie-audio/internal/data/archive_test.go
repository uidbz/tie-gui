package data

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/metadata"
)

// fakeFilehost is a minimal in-memory tie filehost for archive tests: blobs
// are served (and hash-verified by getlib) from a map, HEAD reports presence,
// and PUT /upload/<hash> stores a blob after checking its content address.
type fakeFilehost struct {
	*httptest.Server

	mu      sync.Mutex
	blobs   map[string][]byte
	puts    int // number of PUT uploads received
	zipGets int // GETs of the archive blob (cache assertions)
	zipHash string
}

func newFakeFilehost(t *testing.T) *fakeFilehost {
	t.Helper()
	f := &fakeFilehost{blobs: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/upload/") {
			hash := strings.TrimPrefix(r.URL.Path, "/upload/")
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			got, err := metadata.HashReader(bytes.NewReader(body))
			if err != nil || got != hash {
				http.Error(w, "checksum mismatch", http.StatusBadRequest)
				return
			}
			f.blobs[hash] = body
			f.puts++
			w.Write([]byte(hash)) // the filehost answers with the stored hash
			return
		}
		hash := strings.TrimPrefix(r.URL.Path, "/")
		blob, ok := f.blobs[hash]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet && hash == f.zipHash {
			f.zipGets++
		}
		w.Write(blob) // serves HEAD (headers only) and GET alike
	}))
	t.Cleanup(f.Server.Close)
	return f
}

func (f *fakeFilehost) store(t *testing.T, data []byte) string {
	t.Helper()
	hash, err := metadata.HashReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.blobs[hash] = data
	f.mu.Unlock()
	return hash
}

func (f *fakeFilehost) has(hash string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.blobs[hash]
	return ok
}

// zipOf builds a zip archive in memory.
func zipOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
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
	return buf.Bytes()
}

// tinyJPEG is a real (if small) JPEG so the cover path's image.Decode works.
func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mp3ish carries the ID3 magic archivelib sniffs an audio member by; it is
// not a playable file, so tag parsing fails and the track keeps filename
// labels — which is what these tests assert.
var mp3ish = []byte{'I', 'D', '3', 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

// archiveSession returns a Session whose Host() resolves to the fake
// filehost. The tie client points at a dead triplestore: the cover relation
// write fails and is swallowed (best effort), which the tests exercise too.
func archiveSession(hostURL string) *Session {
	cfg := client.DefaultConfig()
	cfg.TripleStoreURL = "http://127.0.0.1:1"
	cfg.FileHosts = map[string]client.FileHost{"fast": {URL: hostURL}}
	return &Session{Tie: client.NewTieClient(cfg)}
}

// TestArchiveTracksResolution checks the full archive flow: members are
// listed, audio ones extracted, content-hashed and uploaded, non-audio
// members skipped, and the track list comes back in member order with the
// archive as their album.
func TestArchiveTracksResolution(t *testing.T) {
	fh := newFakeFilehost(t)
	track1 := append(mp3ish, []byte("first track body")...)
	track2 := append(mp3ish, []byte("second track body")...)
	cover := tinyJPEG(t)
	zipBlob := zipOf(t, map[string][]byte{
		"02 - Second.mp3": track2,
		"01 - First.mp3":  track1,
		"cover.jpg":       cover,
		"notes.txt":       []byte("not media"),
	})
	zipHash := fh.store(t, zipBlob)
	fh.zipHash = zipHash

	s := archiveSession(fh.URL)
	album := Album{UID: zipHash, Kind: AlbumArchive, Title: "Test Album"}
	tracks, err := s.AlbumTracks(album)
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2: %+v", len(tracks), tracks)
	}
	if tracks[0].Filename != "01 - First.mp3" || tracks[1].Filename != "02 - Second.mp3" {
		t.Errorf("track order = %q, %q; want 01 - First.mp3, 02 - Second.mp3",
			tracks[0].Filename, tracks[1].Filename)
	}
	for i, body := range [][]byte{track1, track2} {
		want := hashOf(t, body)
		if tracks[i].Hash != want {
			t.Errorf("tracks[%d].Hash = %q, want %q", i, tracks[i].Hash, want)
		}
		if tracks[i].AlbumUID != zipHash {
			t.Errorf("tracks[%d].AlbumUID = %q, want the archive hash %q", i, tracks[i].AlbumUID, zipHash)
		}
		if !fh.has(want) {
			t.Errorf("member %d (%s) was not uploaded to the filehost", i, tracks[i].Filename)
		}
	}
	// Two member uploads plus the extracted cover.jpg thumbnail.
	if fh.puts != 3 {
		t.Errorf("filehost saw %d PUT uploads, want 3 (2 members + cover)", fh.puts)
	}

	// Re-opening the album hits the session cache: no further archive
	// download, no further uploads.
	tracks2, err := s.AlbumTracks(album)
	if err != nil {
		t.Fatalf("second AlbumTracks: %v", err)
	}
	if len(tracks2) != 2 || tracks2[0].Hash != tracks[0].Hash {
		t.Error("cached track list differs from the first resolution")
	}
	if fh.zipGets != 1 {
		t.Errorf("archive downloaded %d times, want 1 (session cache)", fh.zipGets)
	}
	if fh.puts != 3 {
		t.Errorf("cache miss after resolution: %d PUTs, want 3", fh.puts)
	}
}

// TestArchiveTracksExistingBlobs checks that members already present on the
// filehost (extracted by an earlier run or another machine) are not
// re-uploaded: only the missing one is PUT.
func TestArchiveTracksExistingBlobs(t *testing.T) {
	fh := newFakeFilehost(t)
	track1 := append(mp3ish, []byte("first track body")...)
	track2 := append(mp3ish, []byte("second track body")...)
	zipBlob := zipOf(t, map[string][]byte{
		"01 - First.mp3":  track1,
		"02 - Second.mp3": track2,
	})
	zipHash := fh.store(t, zipBlob)
	fh.zipHash = zipHash
	fh.store(t, track1) // already extracted by someone else

	s := archiveSession(fh.URL)
	tracks, err := s.AlbumTracks(Album{UID: zipHash, Kind: AlbumArchive})
	if err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if fh.puts != 1 {
		t.Errorf("filehost saw %d PUT uploads, want 1 (only the missing member)", fh.puts)
	}
	if !fh.has(hashOf(t, track2)) {
		t.Error("the missing member was not uploaded")
	}
}

// TestArchiveTracksNoAudio checks that an archive without audio members is an
// error, not an empty album.
func TestArchiveTracksNoAudio(t *testing.T) {
	fh := newFakeFilehost(t)
	zipBlob := zipOf(t, map[string][]byte{"cover.jpg": tinyJPEG(t)})
	zipHash := fh.store(t, zipBlob)

	s := archiveSession(fh.URL)
	_, err := s.AlbumTracks(Album{UID: zipHash, Kind: AlbumArchive})
	if err == nil || !strings.Contains(err.Error(), "no audio") {
		t.Errorf("AlbumTracks = %v, want a no-audio-tracks error", err)
	}
}

// TestArchiveTracksHostCacheKey checks the resolved-track cache is per
// filehost: after a host switch the archive is re-resolved (and lands on the
// new host), because the blobs are only streamable where they were uploaded.
func TestArchiveTracksHostCacheKey(t *testing.T) {
	fh1 := newFakeFilehost(t)
	fh2 := newFakeFilehost(t)
	track1 := append(mp3ish, []byte("first track body")...)
	zipBlob := zipOf(t, map[string][]byte{"01 - First.mp3": track1})
	zipHash := fh1.store(t, zipBlob)
	fh1.zipHash = zipHash
	fh2.store(t, zipBlob)
	fh2.zipHash = zipHash

	s := archiveSession(fh1.URL)
	album := Album{UID: zipHash, Kind: AlbumArchive}
	if _, err := s.AlbumTracks(album); err != nil {
		t.Fatalf("AlbumTracks: %v", err)
	}
	// Switch the session's host to the second filehost.
	s.Tie.Config.FileHosts["fast"] = client.FileHost{URL: fh2.URL}
	if _, err := s.AlbumTracks(album); err != nil {
		t.Fatalf("AlbumTracks after host switch: %v", err)
	}
	if !fh2.has(hashOf(t, track1)) {
		t.Error("member not uploaded to the new filehost after a host switch")
	}
}

func hashOf(t *testing.T, data []byte) string {
	t.Helper()
	h, err := metadata.HashReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
