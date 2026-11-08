package data

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"testing"
)

// flacTag builds a minimal FLAC head: STREAMINFO, an empty Vorbis comment as
// the last block, then tail bytes standing in for audio frames.
func flacTag(tail int) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0x00, 0x00, 34}) // STREAMINFO
	b.Write(make([]byte, 34))
	b.Write([]byte{0x84, 0x00, 0x00, 8}) // last block: Vorbis comment
	b.Write(make([]byte, 8))             // vendorLen=0, commentsLen=0
	b.Write(make([]byte, tail))
	return b.Bytes()
}

// TestTrackEmbeddedCoverProbesOnlyTheTagBlock guards the performance fix: a
// track with 8 MB of audio frames behind its tags must not be downloaded in
// full to find the artwork.
func TestTrackEmbeddedCoverProbesOnlyTheTagBlock(t *testing.T) {
	const headProbe = int64(64 << 10)
	fh := newFakeFilehost(t)
	s := archiveSession(fh.URL)

	fixture, err := os.ReadFile("testdata/track-with-cover.flac")
	if err != nil {
		t.Skip("fixture not present:", err)
	}
	track := append(append([]byte{}, fixture...), make([]byte, 8<<20)...)
	hash := fh.store(t, track)

	got, err := s.trackEmbeddedCover(hash)
	if err != nil {
		t.Fatalf("trackEmbeddedCover: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("trackEmbeddedCover returned no picture")
	}
	if limit := 2 * headProbe; fh.bytesServed > limit {
		t.Errorf("served %d bytes for an 8 MB track, want at most %d", fh.bytesServed, limit)
	}

	// A track with tags but no picture is settled from the same head probe.
	silent := flacTag(8 << 20)
	silentHash := fh.store(t, silent)
	fh.mu.Lock()
	fh.bytesServed = 0
	fh.mu.Unlock()
	got, err = s.trackEmbeddedCover(silentHash)
	if err != nil || got != nil {
		t.Errorf("trackEmbeddedCover(no picture) = (%d bytes, %v), want (nil, nil)", len(got), err)
	}
	fh.mu.Lock()
	served := fh.bytesServed
	fh.mu.Unlock()
	if served > headProbe {
		t.Errorf("served %d bytes for a coverless track, want at most %d", served, headProbe)
	}
}

// TestCoverThumbnailScalesAndUploads checks that a resolved cover is cached as
// a small JPEG on the filehost, so later launches fetch the thumbnail instead
// of the source image.
func TestCoverThumbnailScalesAndUploads(t *testing.T) {
	fh := newFakeFilehost(t)
	s := archiveSession(fh.URL)

	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}

	thumb := s.coverThumbnail("album-uid", buf.Bytes())
	if thumb == nil {
		t.Fatal("coverThumbnail returned nil for a decodable image")
	}
	decoded, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("thumbnail does not decode: %v", err)
	}
	if w, h := decoded.Bounds().Dx(), decoded.Bounds().Dy(); w > coverThumbEdge || h > coverThumbEdge {
		t.Errorf("thumbnail is %dx%d, want both edges <= %d", w, h, coverThumbEdge)
	}
	if fh.puts == 0 {
		t.Error("thumbnail was not uploaded to the filehost")
	}
	hash := hashOf(t, thumb)
	if !fh.has(hash) {
		t.Error("uploaded thumbnail blob not stored under its content hash")
	}
	if got := s.coverThumbnail("album-uid", []byte("not an image")); got != nil {
		t.Errorf("coverThumbnail(garbage) = %d bytes, want nil", len(got))
	}
}
