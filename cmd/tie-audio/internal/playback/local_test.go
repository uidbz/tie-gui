package playback

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pwplayer "github.com/uidbz/pwplay/player"
)

// wavFixture renders a 16-bit PCM WAV of a 440 Hz sine.
func wavFixture(rate, channels, durationSec int) []byte {
	n := rate * durationSec
	data := make([]byte, n*channels*2)
	for i := 0; i < n; i++ {
		v := int16(math.Sin(2*math.Pi*440*float64(i)/float64(rate)) * 32767 * 0.5)
		for ch := 0; ch < channels; ch++ {
			binary.LittleEndian.PutUint16(data[(i*channels+ch)*2:], uint16(v))
		}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(data)))
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))              // fmt chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))               // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(channels))        // channels
	binary.Write(&buf, binary.LittleEndian, uint32(rate))            // sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(rate*channels*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(channels*2))      // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))              // bits
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

// fakeSink satisfies pwplayer.Sink; the test pumps its callback manually to
// simulate the sound card's clock.
type fakeSink struct {
	mu     sync.Mutex
	cb     pwplayer.ProcessCallback
	format pwplayer.Format
}

func (s *fakeSink) Connect(f pwplayer.Format) error {
	s.mu.Lock()
	s.format = f
	s.mu.Unlock()
	return nil
}

func (s *fakeSink) Destroy() {}

func (s *fakeSink) pump(frames int) {
	s.mu.Lock()
	cb, f := s.cb, s.format
	s.mu.Unlock()
	if cb == nil || f.Channels == 0 {
		return
	}
	cb(make([]byte, frames*f.Channels*4), frames)
}

func fakeSinkFactory(s *fakeSink) pwplayer.SinkFactory {
	return func(name string, format pwplayer.Format, cb pwplayer.ProcessCallback, opts pwplayer.SinkOptions) (pwplayer.Sink, error) {
		s.mu.Lock()
		s.cb = cb
		s.mu.Unlock()
		return s, nil
	}
}

// trackServer serves one WAV blob with an explicit Content-Type, mimicking
// the tie filehost the queue URLs point at.
func trackServer(t *testing.T, wav []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(wav)
	}))
}

func newTestBackend(t *testing.T, sink *fakeSink) PlaybackBackend {
	t.Helper()
	b, err := newLocalWithSink(fakeSinkFactory(sink))
	if err != nil {
		t.Fatalf("newLocalWithSink: %v", err)
	}
	t.Cleanup(func() { b.(interface{ Close() error }).Close() })
	return b
}

// waitFor comes from pwplay_test.go (same package).

func TestLocalQueueOps(t *testing.T) {
	srv := trackServer(t, wavFixture(8000, 1, 1))
	defer srv.Close()
	b := newTestBackend(t, &fakeSink{})

	if err := b.Enqueue(srv.URL+"/a", srv.URL+"/b"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	s, _ := b.Status()
	// Queued-but-not-loaded reports track 0, exactly like the pwplay server
	// (same engine); -1 is only for the empty queue (see Clear below).
	if s.TotalTracks != 2 || s.CurrentTrack != 0 {
		t.Fatalf("Status after Enqueue = %+v, want 2 tracks, CurrentTrack 0", s)
	}

	if err := b.Insert(1, srv.URL+"/c"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	s, _ = b.Status()
	want := []string{srv.URL + "/a", srv.URL + "/c", srv.URL + "/b"}
	if s.TotalTracks != 3 || s.Playlist[1] != want[1] {
		t.Fatalf("Playlist after Insert = %v, want %v", s.Playlist, want)
	}

	if err := b.MoveItems(2, 1, 0); err != nil {
		t.Fatalf("MoveItems: %v", err)
	}
	s, _ = b.Status()
	if s.Playlist[0] != srv.URL+"/b" {
		t.Fatalf("Playlist after MoveItems = %v, want b first", s.Playlist)
	}

	if err := b.Remove(0); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	s, _ = b.Status()
	if s.TotalTracks != 2 || s.Playlist[0] != srv.URL+"/a" {
		t.Fatalf("Playlist after Remove = %v, want a,c", s.Playlist)
	}

	if err := b.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	s, _ = b.Status()
	if s.TotalTracks != 0 || s.CurrentTrack != -1 {
		t.Fatalf("Status after Clear = %+v, want empty queue, CurrentTrack -1", s)
	}
}

func TestLocalPlayback(t *testing.T) {
	// The fixture is 10 s of 44.1 kHz stereo: the engine's ring (created
	// before the format is known, 44100x2x3 samples) holds exactly 3 s of
	// it, so it cannot decode in full and the engine keeps reporting
	// Playing. (A slower-rate or shorter track fits the ring whole, hits
	// EOF instantly and flips to stopped while the tail still plays —
	// engine semantics, exercised by TestLocalEndOfQueueStatus below.)
	srv := trackServer(t, wavFixture(44100, 2, 10))
	defer srv.Close()
	sink := &fakeSink{}
	b := newTestBackend(t, sink)
	url := srv.URL + "/track"

	if err := b.PlayAlbum([]string{url}); err != nil {
		t.Fatalf("PlayAlbum: %v", err)
	}
	s, _ := b.Status()
	if s.TotalTracks != 1 || s.Playlist[0] != url {
		t.Fatalf("Status after PlayAlbum = %+v", s)
	}
	waitFor(t, "sink creation (lazy, at track load)", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return sink.format.SampleRate != 0
	})
	sink.mu.Lock()
	format := sink.format
	sink.mu.Unlock()
	if format.SampleRate != 44100 || format.Channels != 2 {
		t.Errorf("sink format = %+v, want {44100 2}", format)
	}

	// Playing + position advancing under the fake clock.
	if s, _ := b.Status(); !s.Playing {
		t.Fatalf("Playing = false after PlayAlbum")
	}
	for i := 0; i < 20; i++ {
		sink.pump(44100 / 20) // ~50 ms each
		time.Sleep(2 * time.Millisecond)
	}
	s, _ = b.Status()
	if s.Position < 0.5 || s.Position > 1.2 {
		t.Errorf("Position = %v, want ~1.0 (pumped)", s.Position)
	}
	if s.TrackDuration <= 0 {
		t.Errorf("TrackDuration = %v, want > 0", s.TrackDuration)
	}
	if s.CurrentTrack != 0 || s.CurrentFile != url {
		t.Errorf("CurrentTrack/File = %d/%q, want 0/%q", s.CurrentTrack, s.CurrentFile, url)
	}

	// Pause holds the position.
	if err := b.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	s, _ = b.Status()
	if !s.Paused || s.Playing {
		t.Errorf("after Pause: Paused=%v Playing=%v", s.Paused, s.Playing)
	}
	pos := s.Position
	sink.pump(44100 / 20)
	s, _ = b.Status()
	if s.Position != pos {
		t.Errorf("position advanced while paused: %v -> %v", pos, s.Position)
	}

	// Seek jumps the reported position.
	if err := b.Seek(0.8); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	if err := b.Play(); err != nil {
		t.Fatalf("Play: %v", err)
	}
	sink.pump(44100 / 20)
	s, _ = b.Status()
	if s.Position < 0.75 {
		t.Errorf("Position after Seek(0.8) = %v, want >= ~0.75", s.Position)
	}

	// Stop rewinds to 0 and reports stopped.
	if err := b.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitFor(t, "stop rewind", func() bool {
		s, _ := b.Status()
		return s.Stopped && s.Position == 0
	})

	// Volume round-trip (the engine stores it as float32 bits).
	if err := b.SetVolume(0.4); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	if s, _ := b.Status(); s.Volume < 0.39 || s.Volume > 0.41 {
		t.Errorf("Volume = %v, want ~0.4", s.Volume)
	}
}

// The repeat-all detector in the UI needs this exact end-of-queue shape:
// stopped on the last track with the position at (near) the track's end.
func TestLocalEndOfQueueStatus(t *testing.T) {
	srv := trackServer(t, wavFixture(8000, 1, 1))
	defer srv.Close()
	sink := &fakeSink{}
	b := newTestBackend(t, sink)

	if err := b.PlayAlbum([]string{srv.URL + "/one"}); err != nil {
		t.Fatalf("PlayAlbum: %v", err)
	}
	waitFor(t, "sink creation", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return sink.format.SampleRate != 0
	})
	// Pump well past the track's duration: the engine stops once the tail
	// drains, leaving the position at the track end.
	for i := 0; i < 40; i++ {
		sink.pump(8000 / 20) // 0.05 s each at 8 kHz
		time.Sleep(2 * time.Millisecond)
	}
	waitFor(t, "stopped at queue end", func() bool {
		s, _ := b.Status()
		return s.Stopped
	})
	s, _ := b.Status()
	if s.CurrentTrack != 0 || s.CurrentTrack != s.TotalTracks-1 {
		t.Errorf("CurrentTrack = %d, want last (0)", s.CurrentTrack)
	}
	if s.TrackDuration <= 0 || s.Position < s.TrackDuration-0.75 {
		t.Errorf("end-of-queue shape: pos=%v dur=%v, want pos >= dur-0.75", s.Position, s.TrackDuration)
	}
}
