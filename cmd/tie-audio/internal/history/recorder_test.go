package history

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
	"github.com/uidbz/tie-gui/internal/tietest"
)

type fakeBackend struct {
	playback.PlaybackBackend
	source string
	recs   []playback.PlayRecord
	asked  []uint64
}

func (f *fakeBackend) History(since uint64) (playback.History, error) {
	f.asked = append(f.asked, since)
	h := playback.History{Source: f.source}
	for _, r := range f.recs {
		if r.Seq > h.LastSeq {
			h.LastSeq = r.Seq
		}
		if r.Seq > since {
			h.Records = append(h.Records, r)
		}
	}
	return h, nil
}

func randHash() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func testSession(t *testing.T) *data.Session {
	t.Helper()
	cfg := tietest.Config()
	conn, err := net.Dial("tcp", "localhost:2161")
	if err != nil {
		t.Skipf("tie test-env not running: %v", err)
	}
	conn.Close()
	return &data.Session{Tie: tieclient.NewTieClient(cfg)}
}

func TestRecorderDrainsClassifiesAndDedups(t *testing.T) {
	sess := testSession(t)
	track, album := randHash(), randHash()
	start := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	be := &fakeBackend{source: "test/" + randHash()[:8], recs: []playback.PlayRecord{
		{Seq: 1, URL: "http://fh/" + track, StartedAt: start, Listened: 150, Duration: 200},                     // complete
		{Seq: 2, URL: "http://fh/" + track, StartedAt: start.Add(time.Minute), Listened: 5, Duration: 200},      // skip
		{Seq: 3, URL: "http://fh/" + track, StartedAt: start.Add(2 * time.Minute), Listened: 70, Duration: 200}, // neither
		{Seq: 4, URL: "http://fh/not-a-hash", StartedAt: start, Listened: 150, Duration: 200},
	}}
	dir := t.TempDir()
	recorded := 0
	opts := Options{
		Dir:        dir,
		Session:    func() *data.Session { return sess },
		Backend:    func() playback.PlaybackBackend { return be },
		BackendKey: func() string { return "fake" },
		Track: func(url string) (data.Track, bool) {
			return data.Track{AlbumUID: album}, true
		},
		OnRecorded: func(n int) { recorded += n },
	}
	r := New(opts)
	n, err := r.Drain()
	if err != nil || n != 2 || recorded != 2 {
		t.Fatalf("drain: n=%d recorded=%d err=%v", n, recorded, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history-spool.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("spool not emptied: %v", err)
	}
	row, err := sess.Tie.Get(album)
	if err != nil {
		t.Fatal(err)
	}
	if s := tieclient.StatsFromRow(row); s.PlayCount != 1 || s.SkipCount != 1 {
		t.Fatalf("album stats %+v", s)
	}

	// A fresh recorder (app restart) resumes from the persisted cursor.
	r2 := New(opts)
	if n, err := r2.Drain(); err != nil || n != 0 {
		t.Fatalf("second drain: %d %v", n, err)
	}
	if last := be.asked[len(be.asked)-1]; last != 4 {
		t.Fatalf("resumed from %d, want 4", last)
	}

	// The server's log was recreated (new source, seq restarted): re-read
	// from 0; the old events are not rewritten (different ids anyway).
	be.source = "test/" + randHash()[:8]
	be.recs = []playback.PlayRecord{{Seq: 1, URL: "http://fh/" + track, StartedAt: start.Add(time.Hour), Listened: 200, Duration: 200}}
	if n, err := r2.Drain(); err != nil || n != 1 {
		t.Fatalf("new source drain: %d %v", n, err)
	}
	row, _ = sess.Tie.Get(track)
	if s := tieclient.StatsFromRow(row); s.PlayCount != 2 {
		t.Fatalf("track stats %+v", s)
	}
}

func TestRecorderKeepsSpoolWhenTieIsDown(t *testing.T) {
	cfg := tietest.Config()
	cfg.TripleStoreURL = "http://127.0.0.1:1" // nothing listens
	for k, c := range cfg.Collections {
		c.TripleStoreURL = cfg.TripleStoreURL
		cfg.Collections[k] = c
	}
	sess := &data.Session{Tie: tieclient.NewTieClient(cfg)}
	be := &fakeBackend{source: "s", recs: []playback.PlayRecord{{Seq: 1, URL: "http://fh/" + randHash(), StartedAt: time.Now(), Listened: 300, Duration: 300}}}
	dir := t.TempDir()
	r := New(Options{
		Dir:        dir,
		Session:    func() *data.Session { return sess },
		Backend:    func() playback.PlaybackBackend { return be },
		BackendKey: func() string { return "fake" },
		Track:      func(string) (data.Track, bool) { return data.Track{}, false },
	})
	if _, err := r.Drain(); err == nil {
		t.Fatal("expected an error from the unreachable triplestore")
	}
	events, err := r.readSpoolFile(r.spoolPath())
	if err != nil || len(events) != 1 {
		t.Fatalf("spool %v %v", events, err)
	}
}
