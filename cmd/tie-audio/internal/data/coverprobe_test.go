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

func TestTagBlockSizeFLAC(t *testing.T) {
	head := flacTag(1024)
	n, ok := tagBlockSize(head)
	if !ok {
		t.Fatal("tagBlockSize(flac) not ok")
	}
	if want := int64(4 + 4 + 34 + 4 + 8); n != want {
		t.Errorf("tagBlockSize(flac) = %d, want %d", n, want)
	}
	if _, ok := tagBlockSize(make([]byte, 100)); ok {
		t.Error("tagBlockSize(garbage) ok, want false")
	}
	// A block body running past the head cannot be measured yet.
	short := flacTag(0)[:20]
	if _, ok := tagBlockSize(short); ok {
		t.Error("tagBlockSize(truncated) ok, want false")
	}
}

func TestTagBlockSizeID3(t *testing.T) {
	head := make([]byte, 10)
	copy(head, "ID3")
	head[3], head[4] = 3, 0 // v2.3
	// Synchsafe size 1000 = 0x00 0x00 0x07 0x68.
	head[6], head[7], head[8], head[9] = 0x00, 0x00, 0x07, 0x68
	n, ok := tagBlockSize(head)
	if !ok || n != 1010 {
		t.Errorf("tagBlockSize(id3) = (%d, %v), want (1010, true)", n, ok)
	}
	head[5] = 0x10 // v2.4 footer present
	if n, _ := tagBlockSize(head); n != 1020 {
		t.Errorf("tagBlockSize(id3+footer) = %d, want 1020", n)
	}
}

// TestEmbeddedCoverInFixture parses the real fixture at its declared tag
// extent, without any of the audio frames behind it.
func TestEmbeddedCoverInFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/track-with-cover.flac")
	if err != nil {
		t.Skip("fixture not present:", err)
	}
	n, ok := tagBlockSize(data)
	if !ok || n > int64(len(data)) {
		t.Fatalf("tagBlockSize(fixture) = (%d, %v), file is %d bytes", n, ok, len(data))
	}
	pic, parsed := embeddedCoverIn(data[:n])
	if !parsed || pic == nil {
		t.Fatalf("embeddedCoverIn(fixture tag block) = (%d bytes, %v), want a picture", len(pic), parsed)
	}
	want, err := os.ReadFile("testdata/archive-src/cover.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pic, want) {
		t.Errorf("embedded picture = %d bytes, want cover.jpg's %d", len(pic), len(want))
	}
}

// TestTrackEmbeddedCoverProbesOnlyTheTagBlock guards the performance fix: a
// track with 8 MB of audio frames behind its tags must not be downloaded in
// full to find the artwork.
func TestTrackEmbeddedCoverProbesOnlyTheTagBlock(t *testing.T) {
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
	if limit := int64(2 * coverHeadBytes); fh.bytesServed > limit {
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
	if served > coverHeadBytes {
		t.Errorf("served %d bytes for a coverless track, want at most %d", served, coverHeadBytes)
	}
}

func TestEmbeddedCoverInUnknownContainer(t *testing.T) {
	// A container the tag parser cannot read (WAV, AIFF, bare MP3 frames) has
	// no picture to find, so the answer is settled without a full download.
	if pic, parsed := embeddedCoverIn(make([]byte, 1024)); !parsed || pic != nil {
		t.Errorf("embeddedCoverIn(garbage) = (%d bytes, %v), want (nil, true)", len(pic), parsed)
	}
	// Truncated tags in a recognised container are inconclusive: more of the
	// file may hold the picture.
	flac := flacTag(0)
	if _, parsed := embeddedCoverIn(flac[:8]); parsed {
		t.Error("embeddedCoverIn(truncated FLAC) parsed, want inconclusive")
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
	// Undecodable bytes are not an image; the caller treats them as no cover.
	if got := s.coverThumbnail("album-uid", []byte("not an image")); got != nil {
		t.Errorf("coverThumbnail(garbage) = %d bytes, want nil", len(got))
	}
}
