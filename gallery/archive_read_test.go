package gallery

import (
	"archive/zip"
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeZip writes an archive with images and non-images, and returns its path.
func makeZip(t *testing.T, images, others int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "many.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	put := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < images; i++ {
		put(imgName(i), pngBuf.Bytes())
	}
	for i := 0; i < others; i++ {
		put("docs/other"+itoa(i)+".bin", []byte("not an image"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func imgName(i int) string { return "pics/img" + itoa(i) + ".png" }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestReadImageArchiveReplaces is the regression for review §4.4: the listing
// used to be appended to, so opening the same archive twice duplicated every
// entry with stale order indices.
func TestReadImageArchiveReplaces(t *testing.T) {
	path := makeZip(t, 6, 3)
	v := &Gallery{}

	v.ReadImageArchive(path)
	first := v.ImageCount()
	if first != 6 {
		t.Fatalf("first read: %d entries, want 6 images (non-members excluded)", first)
	}
	for i, info := range v.imageFiles {
		if info.order != i {
			t.Errorf("entry %d has order %d", i, info.order)
		}
	}

	v.ReadImageArchive(path)
	if second := v.ImageCount(); second != first {
		t.Fatalf("second read: %d entries, want %d (the listing must be replaced)", second, first)
	}

	// An archive read after another listing replaces it rather than appending.
	v.imageFiles = []*ImageInfo{NewImageInfo(0, "/somewhere/else.png")}
	v.ReadImageArchive(path)
	if got := v.ImageCount(); got != first {
		t.Fatalf("read after a preloaded listing: %d entries, want %d", got, first)
	}
}

// TestReadImageArchiveNoFDLeak covers the same finding's other half: every
// member handle must be closed. The sibling walk in LoadGallery closes them;
// this path did not, and the count only stayed bounded because the os.File
// finalizer eventually closed them - a large archive hit EMFILE first.
func TestReadImageArchiveNoFDLeak(t *testing.T) {
	const members = 800
	path := makeZip(t, members/2, members/2)

	before := openFDCount(t)
	v := &Gallery{}
	v.ReadImageArchive(path)
	after := openFDCount(t)

	if grew := after - before; grew > members/4 {
		t.Errorf("ReadImageArchive leaked file descriptors: %d before, %d after (+%d over %d members)",
			before, after, grew, members)
	}
	if v.ImageCount() != members/2 {
		t.Errorf("image count = %d, want %d", v.ImageCount(), members/2)
	}
}

func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("no /proc:", err)
	}
	return len(entries)
}
