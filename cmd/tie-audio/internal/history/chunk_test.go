package history

import (
	"errors"
	"testing"

	tieclient "github.com/uidbz/tie/client"
)

func events(n int) []tieclient.PlayEvent {
	out := make([]tieclient.PlayEvent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, tieclient.PlayEvent{Subject: hashN(i)})
	}
	return out
}

func hashN(i int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, 64)
	for i := range b {
		b[i] = hexdigits[(i*7)%16]
	}
	b[0] = hexdigits[i%16]
	return string(b)
}

// The spool is append-only, so a player that stays offline for weeks builds one
// huge batch. Chunking is what lets the front of the history land when the
// server refuses the whole thing.
func TestRecordChunksBatchesTheSpool(t *testing.T) {
	var sizes []int
	send := func(chunk []tieclient.PlayEvent) (int, error) {
		sizes = append(sizes, len(chunk))
		return len(chunk), nil
	}
	total, left, err := recordChunks(send, events(2*flushChunkSize+5))
	if err != nil {
		t.Fatalf("recordChunks: %v", err)
	}
	if total != 2*flushChunkSize+5 {
		t.Errorf("recorded %d plays, want %d", total, 2*flushChunkSize+5)
	}
	if left != nil {
		t.Errorf("%d events left after a clean flush, want none", len(left))
	}
	want := []int{flushChunkSize, flushChunkSize, 5}
	if len(sizes) != len(want) {
		t.Fatalf("%d calls with sizes %v, want %v", len(sizes), sizes, want)
	}
	for i, w := range want {
		if sizes[i] != w {
			t.Errorf("call %d sent %d events, want %d", i, sizes[i], w)
		}
	}
}

// A failure part-way must report exactly what was not sent, so the spool can be
// rewritten with the remainder. Leaving the spool whole would replay the
// recorded chunks as duplicate plays.
func TestRecordChunksStopsAtFirstFailure(t *testing.T) {
	calls := 0
	send := func(chunk []tieclient.PlayEvent) (int, error) {
		calls++
		if calls == 3 {
			return 0, errors.New("server refused the batch")
		}
		return len(chunk), nil
	}
	all := events(3*flushChunkSize + 10)
	total, left, err := recordChunks(send, all)
	if err == nil {
		t.Fatal("the failing chunk should surface an error")
	}
	if want := 2 * flushChunkSize; total != want {
		t.Errorf("recorded %d plays before the failure, want %d", total, want)
	}
	if want := flushChunkSize + 10; len(left) != want {
		t.Fatalf("%d events left, want %d", len(left), want)
	}
	// The remainder must be the tail, in order: the first un-sent event is the
	// one that follows the last recorded one.
	if left[0].Subject != all[2*flushChunkSize].Subject {
		t.Error("the remainder does not start where the recorded part stopped")
	}
}

func TestRecordChunksEmptySpool(t *testing.T) {
	called := false
	total, left, err := recordChunks(func([]tieclient.PlayEvent) (int, error) {
		called = true
		return 0, nil
	}, nil)
	if err != nil || total != 0 || left != nil || called {
		t.Errorf("an empty spool should not call the backend at all: %d %v %v %v",
			total, left, err, called)
	}
}

// rewriteSpool is the difference between losing history and keeping it: it runs
// after a partial flush, so an interrupted write must not drop the events that
// were never sent.
func TestRewriteSpoolRoundTrip(t *testing.T) {
	r := &Recorder{opts: Options{Dir: t.TempDir()}}
	if err := r.appendSpool(events(5)); err != nil {
		t.Fatal(err)
	}
	if err := r.rewriteSpool(events(5)[2:]); err != nil {
		t.Fatal(err)
	}
	got, err := r.readSpool()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("spool holds %d events after the rewrite, want 3", len(got))
	}
	if err := r.rewriteSpool(nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.readSpool(); len(got) != 0 {
		t.Errorf("rewriting to empty left %d events behind", len(got))
	}
}
