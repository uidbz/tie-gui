package playback

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pwclient "github.com/uidbz/pwplay/client"
)

// fakePwplay is a control-plane-faithful fake of pwplay-server: playlist
// mutations (add/remove) are queued through channels and applied by a loop
// goroutine, like the real decoder loop, so a Status issued right after a
// mutation POST can still show the pre-mutation queue. It lets the tests
// exercise the real pwplayRemote HTTP client without PipeWire or audio files.
type fakePwplay struct {
	mu       sync.Mutex
	playlist []string
	current  int
	loaded   bool
	playing  bool
	paused   bool
	stopped  bool

	add    chan string
	remove chan int
	gotoCh chan int
}

func newFakePwplay() *fakePwplay {
	f := &fakePwplay{
		current: -1,
		stopped: true,
		add:     make(chan string, 64),
		remove:  make(chan int, 64),
		gotoCh:  make(chan int, 64),
	}
	go f.loop()
	return f
}

// loop mimics the decoder loop's control plane: one queued op per iteration.
// Navigation is honored even while paused or stopped, but — like the real
// player — a successful load clears only the stopped flag, not paused.
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
		case g := <-f.gotoCh:
			f.mu.Lock()
			if g >= 0 && g < len(f.playlist) {
				f.current = g
				f.loaded = true
				f.stopped = false
				if !f.paused {
					f.playing = true
				}
			}
			f.mu.Unlock()
		default:
		}
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
}
