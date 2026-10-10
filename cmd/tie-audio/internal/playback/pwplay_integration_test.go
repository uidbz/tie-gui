package playback

// Live end-to-end check of PlayAlbum against the real pwplay-server from the
// tie-audio test-env (cmd/tie-audio/test-env/start.sh), streaming real blobs
// from the tie test-env filehost. The fake in pwplay_test.go models the
// control plane; only the real decoder loop shows the download storm for real:
// it reloads the current index whenever the current track is removed, so the
// old trim-one-at-a-time PlayAlbum downloaded every old track in turn, and its
// early Play was swallowed by the stale stopped flag. Skipped unless the
// test-env is running.

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
	"github.com/uidbz/tie/io/putlib"
)

const (
	realPwplayURL   = "http://localhost:8080"
	realFilehostURL = "http://localhost:2162"
)

// requireRealEnv skips unless the test-env's pwplay-server and filehost answer.
func requireRealEnv(t *testing.T) {
	t.Helper()
	resp, err := http.Get(realPwplayURL + "/status")
	if err != nil {
		t.Skipf("pwplay-server not running (start cmd/tie-audio/test-env/start.sh): %v", err)
	}
	resp.Body.Close()
	resp, err = http.Head(realFilehostURL + "/" + strings.Repeat("0", 64))
	if err != nil {
		t.Skipf("tie test-env filehost not running: %v", err)
	}
	resp.Body.Close()
}

// putBlob makes data fetchable from the test-env filehost (HEAD, then
// PUT /upload/<hash> when absent — the same protocol data.ensureBlob uses) and
// returns its content hash.
func putBlob(t *testing.T, data []byte) string {
	t.Helper()
	var pc putlib.PutConfig
	hash, err := pc.AddressOf(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Head(realFilehostURL + "/" + hash)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return hash
	}
	item := pc.UploadMultipart(realFilehostURL+"/upload/"+hash, bytes.NewReader(data), len(data), "track")
	if item.ErrorMsg != "" {
		t.Fatal(item.ErrorMsg)
	}
	if item.Hash != hash {
		t.Fatalf("checksum mismatch uploading blob %s", hash)
	}
	return hash
}

// realAlbum uploads generated tone tracks (distinct lengths, hence distinct
// content hashes) and returns their filehost stream URLs — extension-less like
// every tie blob, so the engine picks the decoder from the filehost's
// Content-Type. Multi-second tracks (unlike the 0.3 s FLAC fixtures) keep
// playback observable long enough to replace the queue mid-album.
func realAlbum(t *testing.T, durations ...int) []string {
	t.Helper()
	urls := make([]string, 0, len(durations))
	for _, d := range durations {
		urls = append(urls, realFilehostURL+"/"+putBlob(t, wavFixture(44100, 2, d)))
	}
	return urls
}

// pwplayLogPath locates the server's stderr log (env.sh's PWPLAY_LOG, or the
// default location under the test-env).
func pwplayLogPath() string {
	if p := os.Getenv("PWPLAY_LOG"); p != "" {
		return p
	}
	return filepath.Join("..", "..", "test-env", "logs", "pwplay-server.log")
}

func logSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Skipf("pwplay-server log not readable: %v", err)
	}
	return fi.Size()
}

// nowPlayingLines returns the "Now playing" lines the decoder loop logged
// after offset — one per real track load (a full blob download over HTTP).
func nowPlayingLines(t *testing.T, path string, offset int64) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(b[offset:]), "\n") {
		if strings.Contains(line, "Now playing") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func waitForReal(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPwplayRealServerPlayAlbum(t *testing.T) {
	requireRealEnv(t)

	// Two albums of distinct generated tracks, disjoint in content so every
	// hash identifies exactly one track. The first track of each album is
	// longer than the player's ~3 s ring buffer, so the decoder is still on
	// index 0 when the replace lands — the exact state in which the old
	// trim-one-at-a-time path reloaded (downloaded) each old track in turn.
	albumA := realAlbum(t, 8, 4)
	albumB := realAlbum(t, 6, 5)

	b := NewPwplayRemote(pwclient.New(realPwplayURL))
	// Stop, and clear only a non-empty queue: a ClearTracks queued onto an
	// already-empty queue is still pending when PlayAlbum adds, and the
	// decoder loop may apply it *after* the adds and wipe the new album —
	// the very race PlayAlbum guards against.
	_ = b.Stop()
	if s, err := b.Status(); err == nil && len(s.Playlist) > 0 {
		_ = b.Clear()
	}
	waitForReal(t, "empty queue", func() bool {
		s, err := b.Status()
		return err == nil && len(s.Playlist) == 0
	})
	t.Cleanup(func() {
		_ = b.Stop()
		if s, err := b.Status(); err == nil && len(s.Playlist) > 0 {
			_ = b.Clear()
		}
	})

	// 1. From a stopped server, one PlayAlbum must start playback on the first
	//    call: the old implementation fired Play while the mutations were still
	//    in flight, the stale EOF decoder re-set the stopped flag, and nothing
	//    played until a second Play.
	if err := b.PlayAlbum(albumA); err != nil {
		t.Fatal(err)
	}
	waitForReal(t, "album A playing", func() bool {
		s, err := b.Status()
		return err == nil && s.Playing && s.CurrentFile == filepath.Base(albumA[0])
	})

	// 2. Replace the queue while A is playing. Let A's gapless preload of its
	//    second track land first, so it cannot race the log snapshot below.
	time.Sleep(300 * time.Millisecond)
	logPath := pwplayLogPath()
	offset := logSize(t, logPath)
	if err := b.PlayAlbum(albumB); err != nil {
		t.Fatal(err)
	}
	waitForReal(t, "album B playing", func() bool {
		s, err := b.Status()
		return err == nil && s.Playing && playlistEquals(s.Playlist, albumB) &&
			s.CurrentFile == filepath.Base(albumB[0])
	})

	// 3. The replace must not have loaded a single A track: trimming the old
	//    queue one at a time reloads the current index on every removal, i.e.
	//    one full filehost download per old track (the churn the user heard).
	//    Status settles before the loop logs the load (CurrentFile is
	//    playlist-based), so wait for B's first load line, then inspect the
	//    tail — any churn would have logged before that line anyway.
	b0 := filepath.Base(albumB[0])
	var tail string
	deadline := time.Now().Add(5 * time.Second)
	for {
		tail = nowPlayingLines(t, logPath, offset)
		if strings.Contains(tail, b0) || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, url := range albumA {
		if hash := filepath.Base(url); strings.Contains(tail, hash) {
			t.Errorf("PlayAlbum churned the old queue: server loaded %s during the replace:\n%s", hash, tail)
		}
	}
	if !strings.Contains(tail, b0) {
		t.Errorf("server never loaded the new album's first track:\n%s", tail)
	}
}
