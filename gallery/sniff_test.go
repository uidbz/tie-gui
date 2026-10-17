package gallery

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// eofReader hands back the whole payload and io.EOF in the same Read call -
// exactly what a compressed archive member reader does for a small member.
type eofReader struct {
	b   []byte
	got bool
}

func (r *eofReader) Read(p []byte) (int, error) {
	if r.got {
		return 0, io.EOF
	}
	r.got = true
	n := copy(p, r.b)
	return n, io.EOF
}

// shortReader returns a few bytes per call, the other half of the io.Reader
// contract a single Read cannot rely on.
type shortReader struct {
	b []byte
	i int
}

func (r *shortReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.i:min(r.i+3, len(r.b))])
	r.i += n
	return n, nil
}

// TestIsImageHandlesShortAndEOFReads is the regression for review §4.12.
// IsImage used to do one Read and treat any error as a failure, so a member
// that returned its bytes together with io.EOF looked like a non-image -
// browsing an archive listed nothing at all.
func TestIsImageHandlesShortAndEOFReads(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	pngBytes := buf.Bytes()

	if !IsImage(&eofReader{b: pngBytes}) {
		t.Error("IsImage rejected a PNG read in one call that also returned io.EOF")
	}
	if !IsImage(&shortReader{b: pngBytes}) {
		t.Error("IsImage rejected a PNG delivered three bytes at a time")
	}
	if !IsImage(bytes.NewReader(pngBytes)) {
		t.Error("IsImage rejected a plain PNG")
	}
	if IsImage(bytes.NewReader([]byte("just some text, not an image"))) {
		t.Error("IsImage accepted a non-image")
	}
	if IsImage(bytes.NewReader(nil)) {
		t.Error("IsImage accepted an empty reader")
	}
}

func TestIsArchiveFromPathRejectsMissingFile(t *testing.T) {
	if IsArchiveFromPath(filepath.Join(t.TempDir(), "nope.zip")) {
		t.Error("IsArchiveFromPath accepted a nonexistent file")
	}
}

// TestIsArchiveFromPathSmallArchive covers a zip small enough that one Read
// returns it whole.
func TestIsArchiveFromPathSmallArchive(t *testing.T) {
	path := makeZip(t, 1, 0)
	if !IsArchiveFromPath(path) {
		t.Error("IsArchiveFromPath rejected a real zip")
	}
	plain := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(plain, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsArchiveFromPath(plain) {
		t.Error("IsArchiveFromPath accepted a text file")
	}
}

func TestSniffHeadErrors(t *testing.T) {
	if _, err := sniffHead(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Errorf("empty reader: err = %v, want io.EOF", err)
	}
	head, err := sniffHead(bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Errorf("short reader: unexpected error %v", err)
	}
	if string(head) != "abc" {
		t.Errorf("head = %q, want the bytes actually read", head)
	}
}
