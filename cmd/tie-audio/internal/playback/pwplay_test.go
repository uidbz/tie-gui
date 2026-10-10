package playback

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
)

// fakePwplay is a control-plane-faithful fake of pwplay-server: playlist
// mutations (add/remove/clear) are queued through channels and applied by a
// loop goroutine, like the real decoder loop, so a Status issued right after a
// mutation POST can still show the pre-mutation queue. It lets the tests
// exercise the real pwplayRemote HTTP client without PipeWire or audio files.
//
// It also models the engine's track loading: while playing (not paused or
// stopped) with no decoder loaded, the loop "loads" the track at the current
// index — recording every load in loads. Removing the current track clears the
// decoder, so a queue trimmed one track at a time while playing makes the loop
// load each old track in turn; PlayAlbum must not cause that.
type fakePwplay struct {
	mu       sync.Mutex
	playlist []string
	current  int
	loaded   bool
	playing  bool
	paused   bool
	stopped  bool
	loads    []string // URLs the loop loaded, in order (the storm counter)
	clears   int      // /clear requests received (a replace must not clear an empty queue)

	add           chan string
	remove        chan int
	clear         chan struct{}
	gotoCh        chan int
	supportsClear bool // older servers lack /clear
}

func newFakePwplay() *fakePwplay {
	return newFakePwplayWithClear(true)
}

func newFakePwplayWithClear(supports bool) *fakePwplay {
	f := &fakePwplay{
		current:       -1,
		stopped:       true,
		supportsClear: supports,
		add:           make(chan string, 64),
		remove:        make(chan int, 64),
		clear:         make(chan struct{}, 64),
		gotoCh:        make(chan int, 64),
	}
	go f.loop()
	return f
}

// loop mimics the decoder loop's control plane: one queued op per iteration.
// Navigation is honored even while paused or stopped, but — like the real
// player — a successful load clears only the stopped flag, not paused, and
// while stopped or paused no track is ever loaded.
func (f *fakePwplay) loop() {
	for {
		select {
		case u := <-f.add:
			f.mu.Lock()
			f.playlist = append(f.playlist, u)
			f.mu.Unlock()
		case i := <-f.remove:
			f.mu.Lock()
			if i >= 0 && i < len(f.playlist) {
				f.playlist = append(f.playlist[:i], f.playlist[i+1:]...)
				switch {
				case i < f.current:
					f.current--
				case i == f.current:
					f.loaded = false
					if len(f.playlist) == 0 {
						f.current = -1
						f.stopped = true
					} else if f.current >= len(f.playlist) {
						f.current = len(f.playlist) - 1
					}
				}
			}
			f.mu.Unlock()
		case <-f.clear:
			// One decoder-loop step: the whole playlist goes, the decoder is
			// dropped, playback stops.
			f.mu.Lock()
			f.playlist = nil
			f.current = -1
			f.loaded = false
			f.playing = false
			f.stopped = true
			f.mu.Unlock()
		case g := <-f.gotoCh:
			f.mu.Lock()
			if g >= 0 && g < len(f.playlist) {
				f.current = g
				f.loaded = true
				f.loads = append(f.loads, f.playlist[g])
				f.stopped = false
				if !f.paused {
					f.playing = true
				}
			}
			f.mu.Unlock()
		default:
		}

		// The real loop reloads the current index when a decoder is missing
		// and playback is supposed to be running (e.g. right after the
		// current track was removed).
		f.mu.Lock()
		if f.playing && !f.paused && !f.stopped && !f.loaded && len(f.playlist) > 0 {
			if f.current < 0 {
				f.current = 0
			}
			if f.current >= len(f.playlist) {
				f.current = len(f.playlist) - 1
			}
			f.loaded = true
			f.loads = append(f.loads, f.playlist[f.current])
		}
		f.mu.Unlock()

		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fakePwplay) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		file := ""
		if f.loaded && f.current >= 0 && f.current < len(f.playlist) {
			file = f.playlist[f.current]
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"playing":       f.playing,
			"paused":        f.paused,
			"stopped":       f.stopped,
			"currentTrack":  f.current,
			"currentFile":   file,
			"playlist":      f.playlist,
			"totalTracks":   len(f.playlist),
			"position":      0,
			"trackDuration": 0,
			"volume":        1,
		})
	})
	mux.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Paths []string `json:"paths"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		for _, p := range req.Paths {
			f.add <- p
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "added"})
	})
	mux.HandleFunc("/remove", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Index int }
		json.NewDecoder(r.Body).Decode(&req)
		f.remove <- req.Index
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})
	})
	if f.supportsClear {
		mux.HandleFunc("/clear", func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.clears++
			f.mu.Unlock()
			f.clear <- struct{}{}
			json.NewEncoder(w).Encode(map[string]string{"status": "cleared"})
		})
	}
	mux.HandleFunc("/goto", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Index int `json:"index"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.gotoCh <- req.Index
		json.NewEncoder(w).Encode(map[string]string{"status": "goto"})
	})
	mux.HandleFunc("/play", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if len(f.playlist) > 0 {
			if !f.loaded {
				f.loaded = true
				if f.current < 0 {
					f.current = 0
				}
				f.loads = append(f.loads, f.playlist[f.current])
			}
			f.playing = true
			f.paused = false
			f.stopped = false
		}
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": "playing"})
	})
	mux.HandleFunc("/pause", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paused = true
		f.playing = false
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": "paused"})
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.stopped = true
		f.playing = false
		f.paused = false
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	return mux
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Enqueue must not return before the added tracks are visible in a following
// Status: the queue view refreshes immediately after the call, and pwplay
// applies adds asynchronously in its decoder loop.
func TestPwplayEnqueueSettlesBeforeReturning(t *testing.T) {
	fake := newFakePwplay()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	b := NewPwplayRemote(pwclient.New(srv.URL))
	if err := b.Enqueue("http://h/a", "http://h/b", "http://h/c"); err != nil {
		t.Fatal(err)
	}
	s, err := b.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Playlist) != 3 {
		t.Errorf("playlist length right after Enqueue = %d, want 3 (add not settled)", len(s.Playlist))
	}
}

// PlayAlbum over a populated stopped queue must end with exactly the new
// tracks and playback running (no leftover or doubled entries).
func TestPwplayPlayAlbumReplacesStoppedQueue(t *testing.T) {
	fake := newFakePwplay()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	b := NewPwplayRemote(pwclient.New(srv.URL))
	if err := b.Enqueue("http://h/old1", "http://h/old2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "old queue", func() bool {
		s, err := b.Status()
		return err == nil && len(s.Playlist) == 2
	})

	news := []string{"http://h/n1", "http://h/n2", "http://h/n3"}
	if err := b.PlayAlbum(news); err != nil {
		t.Fatal(err)
	}
	s, err := b.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Playing {
		t.Error("status after PlayAlbum: not playing")
	}
	if len(s.Playlist) != 3 || s.Playlist[0] != "http://h/n1" {
		t.Errorf("playlist after PlayAlbum = %v, want the 3 new tracks", s.Playlist)
	}
	fake.mu.Lock()
	clears := fake.clears
	fake.mu.Unlock()
	if clears != 1 {
		t.Errorf("a populated-queue replace sent %d /clear requests, want 1 (the clear path)", clears)
	}
}

// PlayAlbum on an already-empty queue must not send a clear at all: the
// settle wait polls the applied playlist, and an empty queue matches it
// instantly, so a queued clear could still be applied *after* the adds and
// wipe the new album (the loop picks whichever channel is ready).
func TestPwplayPlayAlbumOnEmptyQueueDoesNotClear(t *testing.T) {
	fake := newFakePwplay()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	b := NewPwplayRemote(pwclient.New(srv.URL))
	news := []string{"http://h/n1", "http://h/n2", "http://h/n3"}
	if err := b.PlayAlbum(news); err != nil {
		t.Fatal(err)
	}
	s, err := b.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !playlistEquals(s.Playlist, news) {
		t.Errorf("playlist after PlayAlbum = %v, want the 3 new tracks (a late clear wiped them)", s.Playlist)
	}
	if !s.Playing {
		t.Error("status after PlayAlbum: not playing")
	}
	fake.mu.Lock()
	clears := fake.clears
	fake.mu.Unlock()
	if clears != 0 {
		t.Errorf("PlayAlbum on an empty queue sent %d /clear requests, want 0", clears)
	}
}

// PlayAlbum while the old queue is playing must never load an old track: the
// engine reloads the current index whenever the current track is removed, so
// trimming the old queue one track at a time makes it download every old track
// in turn before reaching the new album. The clear path replaces in one step.
func TestPwplayPlayAlbumDoesNotChurnOldQueueWhilePlaying(t *testing.T) {
	fake := newFakePwplay()
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	b := NewPwplayRemote(pwclient.New(srv.URL))
	if err := b.Enqueue("http://h/old1", "http://h/old2", "http://h/old3"); err != nil {
		t.Fatal(err)
	}
	if err := b.Play(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "old track playing", func() bool {
		s, err := b.Status()
		return err == nil && s.Playing && s.CurrentFile == "http://h/old1"
	})

	news := []string{"http://h/n1", "http://h/n2", "http://h/n3"}
	if err := b.PlayAlbum(news); err != nil {
		t.Fatal(err)
	}
	s, err := b.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Playing || s.CurrentFile != "http://h/n1" {
		t.Errorf("status after PlayAlbum = playing %v current %q, want playing n1", s.Playing, s.CurrentFile)
	}
	fake.mu.Lock()
	loads := append([]string(nil), fake.loads...)
	fake.mu.Unlock()
	for _, l := range loads {
		if strings.HasPrefix(l, "http://h/old2") || strings.HasPrefix(l, "http://h/old3") {
			t.Errorf("PlayAlbum churned the old queue: loop loaded %q (loads: %v)", l, loads)
		}
	}
}

// On a server without /clear the append-then-trim fallback must still settle
// to exactly the new tracks before Play: the final length alone matches
// transiently mid-mutation, which used to let Play fire early and leave the
// player stopped or churning the old queue.
func TestPwplayPlayAlbumFallbackWithoutClear(t *testing.T) {
	fake := newFakePwplayWithClear(false)
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	b := NewPwplayRemote(pwclient.New(srv.URL))
	if err := b.Enqueue("http://h/old1", "http://h/old2", "http://h/old3"); err != nil {
		t.Fatal(err)
	}
	if err := b.Play(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "old track playing", func() bool {
		s, err := b.Status()
		return err == nil && s.Playing
	})

	news := []string{"http://h/n1", "http://h/n2", "http://h/n3"}
	if err := b.PlayAlbum(news); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "queue replaced", func() bool {
		s, err := b.Status()
		return err == nil && playlistEquals(s.Playlist, news) && s.Playing
	})
}
