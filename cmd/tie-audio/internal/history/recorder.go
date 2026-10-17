// Package history moves finished plays from the playback backend's play log
// into tie's play history (tie client/history.go), so they feed the Home page
// and every other tie client.
//
// The backend is the source of truth for what was heard: the pwplay engine
// measures listened time on its output clock (seeks and pauses excluded) and
// keeps a sequence-numbered log — on the pwplay-server when playing remotely
// (so plays made while tie-audio was closed are picked up on the next start),
// in-process (persisted to the state dir) when playing locally. The Recorder
// drains that log, classifies each record (complete / skip / neither, the
// scrobbling rule in client.ClassifyPlay), resolves the album it was played
// from, and writes the events to tie.
//
// Delivery is at-least-once and idempotent: drained events go to a local
// spool file before the per-backend cursor advances, the spool is flushed to
// tie and truncated only on success, and every event's tie subject is derived
// from (source, seq) — so a crash, an offline triplestore or two tie-audio
// instances draining the same server never lose or double-count a play.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// DrainInterval is how often the recorder polls the backend's log when
// nothing kicks it.
const DrainInterval = 60 * time.Second

// Options wires a Recorder to the app. The accessors are called on every
// drain, because the session and backend are replaced when settings change.
type Options struct {
	// Dir holds the spool and cursor files.
	Dir string
	// Session returns the live session (tie client for writing).
	Session func() *data.Session
	// Backend returns the live playback backend.
	Backend func() playback.PlaybackBackend
	// BackendKey identifies the backend for cursor bookkeeping (e.g.
	// "pwplay:http://host:8080" or "local").
	BackendKey func() string
	// Track returns the metadata the app registered for a queue URL, if any
	// (its AlbumUID is the album the track was played from).
	Track func(url string) (data.Track, bool)
	// OnRecorded, when set, is called (from the recorder goroutine) after
	// new plays were written to tie.
	OnRecorded func(n int)
}

type cursor struct {
	Source string `json:"source"`
	Seq    uint64 `json:"seq"`
}

// Recorder drains play logs into tie. Safe for concurrent Kick/Drain.
type Recorder struct {
	opts    Options
	mu      sync.Mutex // serializes Drain
	cursors map[string]cursor
	kick    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	warned  map[string]bool
}

// New builds a recorder; call Start to run it in the background.
func New(opts Options) *Recorder {
	r := &Recorder{
		opts:    opts,
		cursors: map[string]cursor{},
		kick:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		warned:  map[string]bool{},
	}
	r.loadCursors()
	return r
}

func (r *Recorder) spoolPath() string  { return filepath.Join(r.opts.Dir, "history-spool.jsonl") }
func (r *Recorder) cursorPath() string { return filepath.Join(r.opts.Dir, "history-cursors.json") }

// Start runs the drain loop until Stop: on start, every DrainInterval, and
// whenever Kick is called.
func (r *Recorder) Start() {
	go func() {
		defer close(r.done)
		t := time.NewTicker(DrainInterval)
		defer t.Stop()
		r.drainLogged()
		for {
			select {
			case <-r.stop:
				return
			case <-t.C:
			case <-r.kick:
				// Let a burst of track changes settle into one drain.
				select {
				case <-time.After(2 * time.Second):
				case <-r.stop:
					return
				}
			}
			r.drainLogged()
		}
	}()
}

// Kick asks for a drain soon (non-blocking).
func (r *Recorder) Kick() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Stop ends the loop after one final drain, waiting at most timeout.
func (r *Recorder) Stop(timeout time.Duration) {
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
	finished := make(chan struct{})
	go func() {
		<-r.done
		r.drainLogged()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(timeout):
		log.Printf("tie-audio: history: final drain timed out after %v", timeout)
	}
}

func (r *Recorder) drainLogged() {
	if _, err := r.Drain(); err != nil {
		log.Printf("tie-audio: history: %v", err)
	}
}

// Drain pulls new records from the backend's log, spools them, and flushes
// the spool to tie. It returns how many plays were newly recorded in tie.
func (r *Recorder) Drain() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pullErr := r.pull()
	n, flushErr := r.flush()
	return n, errors.Join(pullErr, flushErr)
}

func (r *Recorder) pull() error {
	be := r.opts.Backend()
	hs, ok := be.(playback.HistorySource)
	if !ok {
		return nil
	}
	key := r.opts.BackendKey()
	cur := r.cursors[key]
	h, err := hs.History(cur.Seq)
	if errors.Is(err, playback.ErrNoHistory) {
		if !r.warned[key] {
			r.warned[key] = true
			log.Printf("tie-audio: history: %s keeps no play log (update pwplay-server); plays are not recorded", key)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if h.Source != cur.Source {
		// A different log (a new server, or its log file was recreated):
		// sequence numbers restarted, so read it from the beginning.
		if cur.Seq != 0 {
			if h, err = hs.History(0); err != nil {
				return err
			}
		}
	}
	if len(h.Records) > 0 {
		events := r.convert(h)
		if err := r.appendSpool(events); err != nil {
			return err // cursor stays: the records are re-read next time
		}
	}
	if h.Source != cur.Source || h.LastSeq != cur.Seq {
		r.cursors[key] = cursor{Source: h.Source, Seq: h.LastSeq}
		return r.saveCursors()
	}
	return nil
}

// convert classifies and resolves log records into tie play events,
// dropping records that are neither a play nor a skip, or not tie blobs.
func (r *Recorder) convert(h playback.History) []tieclient.PlayEvent {
	var out []tieclient.PlayEvent
	sess := r.opts.Session()
	for _, rec := range h.Records {
		hash := path.Base(rec.URL)
		if !isHash(hash) {
			continue
		}
		outcome, ok := tieclient.ClassifyPlay(rec.Listened, rec.Duration)
		if !ok {
			continue
		}
		ev := tieclient.PlayEvent{
			Source:   h.Source,
			Seq:      rec.Seq,
			Subject:  hash,
			PlayedAt: rec.StartedAt,
			Listened: rec.Listened,
			Duration: rec.Duration,
			Outcome:  outcome,
			Position: rec.Position,
		}
		if t, ok := r.opts.Track(rec.URL); ok && t.AlbumUID != "" {
			ev.Collection = t.AlbumUID
		} else if sess != nil {
			ev.Collection = sess.TrackAlbum(hash)
		}
		if ev.Collection == hash {
			ev.Collection = "" // a standalone track: no collection
		}
		ev.ID = tieclient.PlayEventID(ev.Source, ev.Seq)
		out = append(out, ev)
	}
	return out
}

func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

func (r *Recorder) appendSpool(events []tieclient.PlayEvent) error {
	if err := os.MkdirAll(r.opts.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.spoolPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (r *Recorder) readSpool() ([]tieclient.PlayEvent, error) {
	f, err := os.Open(r.spoolPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []tieclient.PlayEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e tieclient.PlayEvent
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Subject != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// flushChunkSize bounds one RecordPlays call. The spool is append-only, so
// being offline for weeks turns it into a single enormous request that the
// server refuses — and a refused request keeps the whole spool, so nothing ever
// gets recorded. Chunking lets the front of the history land even if the tail
// keeps failing.
const flushChunkSize = 200

// flush writes the spooled events to tie in chunks and empties the spool on
// success.
func (r *Recorder) flush() (int, error) {
	events, err := r.readSpool()
	if err != nil || len(events) == 0 {
		return 0, err
	}
	sess := r.opts.Session()
	if sess == nil || sess.Tie == nil {
		return 0, nil
	}
	total, left, err := recordChunks(sess.Tie.RecordPlays, events)
	if len(left) != len(events) {
		// Rewrite the spool with only what has NOT been recorded. Leaving it
		// whole would replay the chunks that already succeeded into tie as
		// duplicate plays on the next drain.
		if rerr := r.rewriteSpool(left); rerr != nil {
			return total, rerr
		}
	}
	if err != nil {
		return total, err // the rest is retried on the next drain
	}
	if err := os.Remove(r.spoolPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return total, err
	}
	if total > 0 && r.opts.OnRecorded != nil {
		r.opts.OnRecorded(total)
	}
	return total, nil
}

// recordChunks hands events to send in flushChunkSize batches. It stops at the
// first failure and returns how many plays landed, the events that were never
// sent, and the error.
func recordChunks(send func([]tieclient.PlayEvent) (int, error), events []tieclient.PlayEvent) (int, []tieclient.PlayEvent, error) {
	total := 0
	for len(events) > 0 {
		chunk := events
		if len(chunk) > flushChunkSize {
			chunk = events[:flushChunkSize]
		}
		n, err := send(chunk)
		if err != nil {
			return total, events, err
		}
		total += n
		events = events[len(chunk):]
	}
	return total, nil, nil
}

// rewriteSpool replaces the spool with the events that still need recording,
// via a temp file and a rename so an interrupted write cannot lose history.
func (r *Recorder) rewriteSpool(events []tieclient.PlayEvent) error {
	if len(events) == 0 {
		return os.Remove(r.spoolPath())
	}
	tmp := r.spoolPath() + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, r.spoolPath())
}

func (r *Recorder) loadCursors() {
	b, err := os.ReadFile(r.cursorPath())
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &r.cursors)
	if r.cursors == nil {
		r.cursors = map[string]cursor{}
	}
}

func (r *Recorder) saveCursors() error {
	if err := os.MkdirAll(r.opts.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r.cursors, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.cursorPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.cursorPath())
}
