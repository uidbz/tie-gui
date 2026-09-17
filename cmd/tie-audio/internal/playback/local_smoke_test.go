//go:build linux && !android

package playback

import (
	"os"
	"testing"
	"time"
)

// TestLocalSmokeRealSink plays a short WAV through the platform sink
// (PipeWire) — the default, no injected fake — and verifies the position
// advances, which only happens when the audio server actually pulls samples.
// Skipped unless TIE_AUDIO_SMOKE=1 (needs a running sound server).
func TestLocalSmokeRealSink(t *testing.T) {
	if os.Getenv("TIE_AUDIO_SMOKE") != "1" {
		t.Skip("set TIE_AUDIO_SMOKE=1 to play audible audio through the real sink")
	}
	srv := trackServer(t, wavFixture(44100, 2, 2))
	defer srv.Close()

	b, err := NewLocal()
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	defer b.(interface{ Close() error }).Close()

	if err := b.PlayAlbum([]string{srv.URL + "/track.wav"}); err != nil {
		t.Fatalf("PlayAlbum: %v", err)
	}
	// The position follows the sink's pull clock; without a running PipeWire
	// it never advances.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := b.Status()
		if s.Position > 0.2 {
			t.Logf("position advanced to %v — real sink is playing", s.Position)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s, _ := b.Status()
	t.Fatalf("position did not advance (status %+v) — is PipeWire running?", s)
}
