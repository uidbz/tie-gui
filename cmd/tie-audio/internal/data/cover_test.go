package data

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestTrackEmbeddedCover exercises the audio-file probe against the fake
// filehost: a track with an embedded picture yields its bytes, a track
// without one yields (nil, nil), an unfetchable blob is an error (so the
// caller retries), and an untaggable blob is (nil, nil) — the download
// succeeded, so there is nothing to retry.
func TestTrackEmbeddedCover(t *testing.T) {
	fh := newFakeFilehost(t)
	s := archiveSession(fh.URL)

	withPic, err := os.ReadFile("testdata/track-with-cover.flac")
	if err != nil {
		t.Skip("fixture not present:", err)
	}
	got, err := s.trackEmbeddedCover(fh.store(t, withPic))
	if err != nil {
		t.Fatalf("trackEmbeddedCover: %v", err)
	}
	want, err := os.ReadFile("testdata/archive-src/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("embedded picture = %d bytes, want the %d bytes of cover.jpg", len(got), len(want))
	}

	plain, err := os.ReadFile("testdata/archive-src/02 - Track 2.flac")
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.trackEmbeddedCover(fh.store(t, plain))
	if err != nil || got != nil {
		t.Errorf("track without picture = (%d bytes, %v), want (nil, nil)", len(got), err)
	}

	got, err = s.trackEmbeddedCover(fh.store(t, mp3ish))
	if err != nil || got != nil {
		t.Errorf("untaggable blob = (%d bytes, %v), want (nil, nil)", len(got), err)
	}

	if _, err = s.trackEmbeddedCover(strings.Repeat("0", 64)); err == nil {
		t.Error("missing blob: want an error so the probe is retried")
	}
}
