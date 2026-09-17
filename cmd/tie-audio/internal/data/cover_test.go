package data

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/uidbz/tie/client"
)

func TestDirCoverHash(t *testing.T) {
	img := func(name string) client.File {
		return client.File{Filename: name, Uid: "uid-" + name, TieType: client.TieImageFile}
	}
	audio := client.File{Filename: "01 - t.flac", Uid: "uid-audio", TieType: client.TieAudioFile}
	tests := []struct {
		name  string
		files []client.File
		want  string
	}{
		{"no files", nil, ""},
		{"no images", []client.File{audio}, ""},
		{"first image without a preferred name", []client.File{img("back.jpg"), img("other.jpg")}, "uid-back.jpg"},
		{"cover preferred over an earlier plain image", []client.File{img("zzz.jpg"), img("cover.jpg")}, "uid-cover.jpg"},
		{"front preferred", []client.File{img("aaa.jpg"), img("front.png")}, "uid-front.png"},
		{"case-insensitive prefix", []client.File{img("Cover.JPG")}, "uid-Cover.JPG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dirCoverHash(client.Directory{Files: tt.files}); got != tt.want {
				t.Errorf("dirCoverHash = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstTrackHash(t *testing.T) {
	track := func(name string) client.File {
		return client.File{Filename: name, Uid: "uid-" + name, TieType: client.TieAudioFile}
	}
	tests := []struct {
		name  string
		files []client.File
		want  string
	}{
		{"no files", nil, ""},
		{"images skipped", []client.File{{Filename: "cover.jpg", Uid: "uid-img", TieType: client.TieImageFile}}, ""},
		{"filename order, not listing order", []client.File{track("02 - b.flac"), track("01 - a.flac")}, "uid-01 - a.flac"},
		{"media-type fallback", []client.File{{Filename: "x.mp3", Uid: "uid-m", MediaType: "audio/mpeg"}}, "uid-m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstTrackHash(client.Directory{Files: tt.files}); got != tt.want {
				t.Errorf("firstTrackHash = %q, want %q", got, tt.want)
			}
		})
	}
}

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
