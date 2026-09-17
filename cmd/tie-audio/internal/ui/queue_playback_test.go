package ui

import (
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// scriptBackend is a playback fake whose Status reflects a scripted state and
// whose control calls mutate it the way pwplay does: Goto loads the track
// (currentTrack follows) but leaves a paused state paused; Play resumes.
type scriptBackend struct {
	fakeBackend

	mu     sync.Mutex
	status playback.Status
	gotos  []int
	plays  int
}

func (b *scriptBackend) Goto(i int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gotos = append(b.gotos, i)
	// pwplay's Goto loads the track and clears stopped, but not paused.
	b.status.CurrentTrack = i
	b.status.Stopped = false
	if !b.status.Paused {
		b.status.Playing = true
	}
	return nil
}

func (b *scriptBackend) Play() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.plays++
	b.status.Playing = true
	b.status.Paused = false
	b.status.Stopped = false
	return nil
}

func (b *scriptBackend) Status() (playback.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, nil
}

func (b *scriptBackend) playCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.plays
}

func newTestQueuePage(backend playback.PlaybackBackend) *queuePage {
	win := test.NewWindow(nil)
	session := &data.Session{Backend: backend}
	transport := newPlayer(backend, nil)
	return newQueuePage(win, session, transport, nil, false, func() {}, nil)
}

func waitForCond(t *testing.T, what string, cond func() bool) {
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

// A double-tap on a queue row while the player is paused must resume playback:
// pwplay's Goto loads the track but does not clear the paused flag, so the
// queue page issues a Play once the jump has landed.
func TestQueuePlayRowResumesFromPause(t *testing.T) {
	test.NewApp()
	backend := &scriptBackend{status: playback.Status{
		Paused:       true,
		CurrentTrack: 0,
		Playlist:     []string{"u1", "u2", "u3"},
		TotalTracks:  3,
	}}
	q := newTestQueuePage(backend)
	q.playlist = []string{"u1", "u2", "u3"}

	q.playRow(1)

	waitForCond(t, "Play after the jump landed", func() bool { return backend.playCount() == 1 })
}

// When the jump target is already playing, no extra Play is issued.
func TestQueuePlayRowNoExtraPlayWhenPlaying(t *testing.T) {
	test.NewApp()
	backend := &scriptBackend{status: playback.Status{
		Playing:      true,
		CurrentTrack: 1,
		Playlist:     []string{"u1", "u2", "u3"},
		TotalTracks:  3,
	}}
	q := newTestQueuePage(backend)
	q.playlist = []string{"u1", "u2", "u3"}

	q.playRow(1)

	// Give the playRow goroutine time to (not) act, then check no Play came.
	time.Sleep(300 * time.Millisecond)
	if n := backend.playCount(); n != 0 {
		t.Errorf("Play calls = %d, want 0 for an already-playing row", n)
	}
}

// noteEnqueued shows the added tracks immediately and shields them from a
// stale mid-mutation poll until endQueueMutation reconciles.
func TestQueueNoteEnqueuedIsOptimisticAndSticky(t *testing.T) {
	test.NewApp()
	backend := &scriptBackend{status: playback.Status{
		CurrentTrack: -1,
		Playlist:     []string{"u1"},
		TotalTracks:  1,
	}}
	q := newTestQueuePage(backend)
	q.playlist = []string{"u1"}

	q.noteEnqueued([]string{"u2", "u3"})
	if got := len(q.playlist); got != 3 {
		t.Fatalf("playlist after noteEnqueued = %d entries, want 3 immediately", got)
	}

	// A poll still showing the pre-add server state must not flicker the
	// optimistically added tracks back out.
	q.applyStatus(playback.Status{CurrentTrack: -1, Playlist: []string{"u1"}, TotalTracks: 1})
	if got := len(q.playlist); got != 3 {
		t.Fatalf("stale poll dropped the optimistic tracks: playlist = %d entries, want 3", got)
	}

	// Once the server has settled, endQueueMutation reconciles with it.
	backend.mu.Lock()
	backend.status.Playlist = []string{"u1", "u2", "u3"}
	backend.status.TotalTracks = 3
	backend.mu.Unlock()
	q.endQueueMutation()
	waitForCond(t, "reconciled playlist", func() bool {
		return q.pending == 0 && len(q.playlist) == 3 && q.playlist[2] == "u3"
	})
}

// noteQueueReplaced swaps the visible queue instantly and shields it from
// mid-replace polls (the server transiently holds old+new tracks).
func TestQueueNoteQueueReplacedIsOptimisticAndSticky(t *testing.T) {
	test.NewApp()
	backend := &scriptBackend{status: playback.Status{
		Playing:      true,
		CurrentTrack: 3,
		Playlist:     []string{"o1", "o2", "o3", "o4"},
		TotalTracks:  4,
	}}
	q := newTestQueuePage(backend)
	q.playlist = []string{"o1", "o2", "o3", "o4"}
	q.current = 3

	q.noteQueueReplaced([]string{"n1", "n2"})
	if got := len(q.playlist); got != 2 || q.playlist[0] != "n1" {
		t.Fatalf("playlist after noteQueueReplaced = %v, want [n1 n2]", q.playlist)
	}
	if q.current != -1 {
		t.Errorf("current after noteQueueReplaced = %d, want -1 (not observed yet)", q.current)
	}

	// A mid-replace poll (old tracks still present server-side) is skipped.
	q.applyStatus(playback.Status{Playing: true, CurrentTrack: 3, Playlist: []string{"o3", "o4", "n1", "n2"}, TotalTracks: 4})
	if got := len(q.playlist); got != 2 {
		t.Fatalf("mid-replace poll replaced the optimistic queue: %d entries, want 2", got)
	}

	backend.mu.Lock()
	backend.status.Playlist = []string{"n1", "n2"}
	backend.status.TotalTracks = 2
	backend.status.CurrentTrack = 0
	backend.mu.Unlock()
	q.endQueueMutation()
	waitForCond(t, "reconciled queue", func() bool {
		return q.pending == 0 && q.current == 0 && len(q.playlist) == 2
	})
}
