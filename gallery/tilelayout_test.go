package gallery

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

func testTile() *Tile {
	img := canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 100, 150)))
	t := &Tile{Content: img}
	t.width = 100
	t.height = 150
	return t
}

// PlaceTiles resets layout.tiles synchronously while grid objects are
// replaced via fyne.Do, so Layout can transiently see object/tile lists of
// different lengths. It must not panic (regression: index out of range).
func TestLayoutToleratesTileObjectMismatch(t *testing.T) {
	config := Config{}
	config.General.TileWidth = 300
	config.General.TileGap = 5
	layout := &TileLayout{config: config}

	newObjects := func(n int) []fyne.CanvasObject {
		objects := make([]fyne.CanvasObject, n)
		for i := range objects {
			objects[i] = canvas.NewRectangle(color.Black)
		}
		return objects
	}

	size := fyne.NewSize(1000, 1000)

	// More objects than tiles (stale grid during a gallery reload).
	layout.tiles = []*Tile{testTile(), testTile(), testTile()}
	layout.Layout(newObjects(5), size)

	// More tiles than objects.
	layout.tiles = []*Tile{testTile(), testTile(), testTile(), testTile(), testTile()}
	layout.Layout(newObjects(3), size)

	// Matching lists.
	objects := newObjects(5)
	layout.Layout(objects, size)

	// Empty grid.
	layout.tiles = nil
	layout.Layout(nil, size)
}

type errReader struct{}

func (errReader) GetReader() (io.ReadSeeker, error) {
	return bytes.NewReader([]byte("this is not an image")), nil
}
func (errReader) Path() string { return "err" }

func writeTestJPEG(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stdraw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, stdraw.Src)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

func testDirLayout(thumbDir string) *TileLayout {
	layout := &TileLayout{}
	layout.config.General.TileWidth = 300
	layout.config.General.ThumbnailDir = thumbDir
	return layout
}

func testJPEGBytes(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stdraw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, stdraw.Src)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// memReader is an in-memory CustomReader for preview tests.
type memReader struct {
	path string
	data []byte
}

func (m memReader) GetReader() (io.ReadSeeker, error) { return bytes.NewReader(m.data), nil }
func (m memReader) Path() string                      { return m.path }

func makeTestZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// A directory listing must surface an archive as an entry carrying
// ShowArchive + FullPath (what the tap handler uses to open it) and all
// image members as swipeable previews.
func TestReadImageDirArchiveEntry(t *testing.T) {
	tmp := t.TempDir()
	writeTestJPEG(t, filepath.Join(tmp, "photo.jpg"), 100, 80, color.RGBA{200, 30, 30, 255})
	zipPath := filepath.Join(tmp, "pack.zip")
	makeTestZip(t, zipPath, map[string][]byte{
		"pics/a.jpg":     testJPEGBytes(t, 100, 80, color.RGBA{30, 200, 30, 255}),
		"pics/b.jpg":     testJPEGBytes(t, 100, 80, color.RGBA{30, 30, 200, 255}),
		"pics/notes.txt": []byte("not an image"),
	})

	v := &Gallery{}
	v.ReadImageDir(tmp, nil)

	var arch *ImageInfo
	for _, fi := range v.imageFiles {
		if fi.ShowArchive {
			arch = fi
		}
	}
	if arch == nil {
		t.Fatal("no archive entry in directory listing")
	}
	if arch.FullPath != zipPath {
		t.Fatalf("FullPath = %q, want %q", arch.FullPath, zipPath)
	}
	if !arch.InputIsDir || !arch.InputIsArchive {
		t.Fatalf("InputIsDir=%v InputIsArchive=%v, want both true", arch.InputIsDir, arch.InputIsArchive)
	}
	if len(arch.PreviewPaths) != 2 {
		t.Fatalf("PreviewPaths = %d, want 2 (txt member skipped)", len(arch.PreviewPaths))
	}
	if !arch.HasPreviews() || arch.PreviewCount() != 2 {
		t.Fatalf("HasPreviews=%v PreviewCount=%d", arch.HasPreviews(), arch.PreviewCount())
	}

	// Members must be readable after ReadImageDir returns (regression: the
	// archive file was closed on return, breaking every later member read).
	layout := testDirLayout(filepath.Join(tmp, "cache"))
	data, err := layout.dirPreviewThumbnail(arch)
	if err != nil {
		t.Fatalf("dirPreviewThumbnail: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Fatalf("archive tile preview is not a JPEG: %v", err)
	}
}

// Opening an archive directly lists its image members as regular image
// entries (no folder badge, no swipe overlay).
func TestReadImageArchiveMembers(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "pack.zip")
	makeTestZip(t, zipPath, map[string][]byte{
		"pics/b.jpg":     testJPEGBytes(t, 100, 80, color.RGBA{30, 200, 30, 255}),
		"pics/a.jpg":     testJPEGBytes(t, 100, 80, color.RGBA{30, 30, 200, 255}),
		"pics/notes.txt": []byte("not an image"),
	})

	v := &Gallery{}
	v.ReadImageArchive(zipPath)

	if len(v.imageFiles) != 2 {
		t.Fatalf("entries = %d, want 2", len(v.imageFiles))
	}
	for i, fi := range v.imageFiles {
		if !fi.InputIsArchive {
			t.Fatalf("entry %d InputIsArchive=false", i)
		}
		if fi.HasPreviews() {
			t.Fatalf("entry %d should not have swipe previews", i)
		}
		// Regression: members must be readable after ReadImageArchive
		// returns (the archive file used to be closed on return).
		r, err := fi.GetReader()
		if err != nil {
			t.Fatalf("entry %d GetReader: %v", i, err)
		}
		b, err := io.ReadAll(r)
		if err != nil || len(b) == 0 {
			t.Fatalf("entry %d read %d bytes, err=%v", i, len(b), err)
		}
	}
	// Sorted by member path.
	if v.imageFiles[0].Path != "pics/a.jpg" || v.imageFiles[1].Path != "pics/b.jpg" {
		t.Fatalf("entries not sorted: %q, %q", v.imageFiles[0].Path, v.imageFiles[1].Path)
	}
}

// memPreviewProvider adds a PreviewProvider implementation to memReader.
type memPreviewProvider struct {
	memReader
	previews []CustomReader
}

func (m memPreviewProvider) Previews() ([]CustomReader, error) { return m.previews, nil }

// memThumbnailer records GetThumbnail calls and returns a pre-scaled image.
type memThumbnailer struct {
	data   []byte
	called int
}

func (m *memThumbnailer) GetThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	m.called++
	return bytes.NewReader(m.data), nil
}

// memCoverProvider combines PreviewProvider + CoverProvider for tests,
// tracking calls to verify which path produced a thumbnail.
type memCoverProvider struct {
	memReader
	previews       []CustomReader
	previewsCalled int
	cover          []byte
	coverErr       error
	stored         [][]byte
}

func (m *memCoverProvider) Previews() ([]CustomReader, error) {
	m.previewsCalled++
	return m.previews, nil
}

func (m *memCoverProvider) CoverThumbnail() (io.ReadSeeker, error) {
	if m.coverErr != nil {
		return nil, m.coverErr
	}
	return bytes.NewReader(m.cover), nil
}

func (m *memCoverProvider) StoreCoverThumbnail(jpegBytes []byte) {
	m.stored = append(m.stored, jpegBytes)
}

// A CoverProvider cover serves the initial tile view WITHOUT enumerating the
// collection (Previews must not be called) and is badged + disk-cached.
func TestDirPreviewThumbnailCoverHit(t *testing.T) {
	tmp := t.TempDir()
	cover := testJPEGBytes(t, 600, 400, color.RGBA{30, 200, 30, 255})
	raw := testJPEGBytes(t, 1200, 800, color.RGBA{200, 30, 30, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	cp := &memCoverProvider{
		memReader: memReader{strings.Repeat("a", 64), nil},
		previews:  []CustomReader{memReader{"m1", raw}},
		cover:     cover,
	}
	info := NewImageInfoCustomReader(0, cp)

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatalf("dirPreviewThumbnail: %v", err)
	}
	if cp.previewsCalled != 0 {
		t.Fatalf("Previews called %d times on cover hit, want 0", cp.previewsCalled)
	}
	if len(cp.stored) != 0 {
		t.Fatalf("StoreCoverThumbnail called %d times on cover hit, want 0", len(cp.stored))
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w != 600 {
		t.Fatalf("cover width = %d, want 600", w)
	}
	// Folder badge: darkened icon area, untouched far area (green cover).
	_, gi, _, _ := img.At(179, 45).RGBA()
	_, ga, _, _ := img.At(500, 300).RGBA()
	if gi>>8 > 150 {
		t.Fatalf("icon area not darkened: G=%d", gi>>8)
	}
	if ga>>8 < 150 {
		t.Fatalf("non-icon area lost source color: G=%d", ga>>8)
	}

	// Second call hits the local disk cache: break the cover and verify the
	// result still comes back without another cover fetch or enumeration.
	cp.cover, cp.coverErr = nil, errors.New("gone")
	data2, err := layout.dirPreviewThumbnail(info)
	if err != nil || !bytes.Equal(data, data2) {
		t.Fatalf("disk-cache hit failed: err=%v equal=%v", err, bytes.Equal(data, data2))
	}
	if cp.previewsCalled != 0 {
		t.Fatalf("Previews called on cache hit")
	}
}

// Without a cover, the preview-readers path generates the thumbnail and
// stores the plain (pre-badge) JPEG as the cover for next time. Swiping
// (previewIndex > 0) never touches the cover path.
func TestDirPreviewThumbnailCoverFallback(t *testing.T) {
	tmp := t.TempDir()
	rawA := testJPEGBytes(t, 1200, 900, color.RGBA{200, 30, 30, 255})
	rawB := testJPEGBytes(t, 800, 800, color.RGBA{30, 30, 200, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	cp := &memCoverProvider{
		memReader: memReader{strings.Repeat("b", 64), nil},
		previews: []CustomReader{
			memReader{"arch/a.jpg", rawA},
			memReader{"arch/b.jpg", rawB},
		},
		coverErr: errors.New("no cover yet"),
	}
	info := NewImageInfoCustomReader(0, cp)

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatalf("dirPreviewThumbnail: %v", err)
	}
	if cp.previewsCalled != 1 {
		t.Fatalf("Previews called %d times, want 1", cp.previewsCalled)
	}
	if len(cp.stored) != 1 {
		t.Fatalf("StoreCoverThumbnail called %d times, want 1", len(cp.stored))
	}
	// The stored cover is the PLAIN member thumbnail (no badge: the red
	// source color is intact in the icon area).
	storedImg, err := jpeg.Decode(bytes.NewReader(cp.stored[0]))
	if err != nil {
		t.Fatal(err)
	}
	if w := storedImg.Bounds().Dx(); w != 600 {
		t.Fatalf("stored cover width = %d, want 600", w)
	}
	rs, _, _, _ := storedImg.At(179, 45).RGBA()
	if rs>>8 < 150 {
		t.Fatal("stored cover should be unbadged (red channel intact)")
	}
	// The returned tile thumbnail IS badged (icon darkens the red).
	img, _ := jpeg.Decode(bytes.NewReader(data))
	rt, _, _, _ := img.At(179, 45).RGBA()
	if rt>>8 > 150 {
		t.Fatalf("tile thumbnail not badged: R=%d", rt>>8)
	}

	// Swipe to index 1: per-member path, no cover store.
	info.previewIndex = 1
	if _, err := layout.dirPreviewThumbnail(info); err != nil {
		t.Fatal(err)
	}
	if len(cp.stored) != 1 {
		t.Fatalf("StoreCoverThumbnail called on swipe, stored=%d", len(cp.stored))
	}
}

// Reader-backed previews (tie directories, archive blobs) are thumbnailed
// through the same pipeline as path-based previews, cached under a key
// derived from the reader's Path.
func TestDirPreviewThumbnailReaders(t *testing.T) {
	tmp := t.TempDir()
	imgA := testJPEGBytes(t, 1200, 900, color.RGBA{200, 30, 30, 255})
	imgB := testJPEGBytes(t, 800, 800, color.RGBA{30, 30, 200, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	info := NewImageInfoCustomReader(0, memPreviewProvider{memReader{"dir", nil}, nil})
	info.PreviewReaders = []CustomReader{
		memReader{"dir/a.jpg", imgA},
		memReader{"dir/b.jpg", imgB},
	}

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatalf("dirPreviewThumbnail: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 600 || h != 450 {
		t.Fatalf("thumbnail size = %dx%d, want 600x450", w, h)
	}

	info.previewIndex = 1
	dataB, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(data, dataB) {
		t.Fatal("different preview readers produced identical thumbnails")
	}
}

// When the layout has a Thumbnailer, reader-based previews come from it
// (already scaled) instead of the raw reader bytes.
func TestDirPreviewThumbnailReadersUsesThumbnailer(t *testing.T) {
	tmp := t.TempDir()
	rawBytes := testJPEGBytes(t, 1200, 900, color.RGBA{200, 30, 30, 255})  // red, full size
	thumbBytes := testJPEGBytes(t, 600, 450, color.RGBA{30, 30, 200, 255}) // blue, pre-scaled

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	th := &memThumbnailer{data: thumbBytes}
	layout.thumbnailer = th

	info := NewImageInfoCustomReader(0, memPreviewProvider{memReader{"dir", nil}, nil})
	info.PreviewReaders = []CustomReader{memReader{"dir/a.jpg", rawBytes}}

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	if th.called != 1 {
		t.Fatalf("thumbnailer called %d times, want 1", th.called)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w != 600 {
		t.Fatalf("thumbnail width = %d, want 600 (pre-scaled, not re-scaled)", w)
	}
	// The blue thumbnail (not the red raw bytes) must have been used.
	_, _, b, _ := img.At(500, 400).RGBA()
	if b>>8 < 150 {
		t.Fatal("thumbnailer image not used")
	}
}

func TestPreviewCountAndHasPreviews(t *testing.T) {
	video := NewImageInfo(0, "/v.mp4")
	video.InputIsVideo = true
	if !video.HasPreviews() || video.PreviewCount() != videoPreviewFrames {
		t.Fatalf("video: HasPreviews=%v PreviewCount=%d", video.HasPreviews(), video.PreviewCount())
	}

	dir := NewImageInfo(0, "/d/a.jpg")
	dir.PreviewPaths = []string{"/d/a.jpg", "/d/b.jpg"}
	if !dir.HasPreviews() || dir.PreviewCount() != 2 {
		t.Fatalf("dir: HasPreviews=%v PreviewCount=%d", dir.HasPreviews(), dir.PreviewCount())
	}

	tie := NewImageInfoCustomReader(0, memPreviewProvider{memReader{"uid", nil}, nil})
	if !tie.HasPreviews() {
		t.Fatal("PreviewProvider reader should have previews")
	}
	if tie.PreviewCount() != 0 {
		t.Fatalf("PreviewCount before lazy load = %d, want 0", tie.PreviewCount())
	}
	tie.PreviewReaders = []CustomReader{memReader{"h", nil}}
	if tie.PreviewCount() != 1 {
		t.Fatalf("PreviewCount = %d, want 1", tie.PreviewCount())
	}

	plain := NewImageInfo(0, "/img.jpg")
	if plain.HasPreviews() || plain.PreviewCount() != 0 {
		t.Fatalf("plain image: HasPreviews=%v PreviewCount=%d", plain.HasPreviews(), plain.PreviewCount())
	}
}

// A directory tile thumbnail is the preview image at 2×TileWidth wide with a
// folder icon darkening the top-left corner.
func TestDirPreviewThumbnailGenerates(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "a.jpg")
	writeTestJPEG(t, src, 1200, 900, color.RGBA{200, 30, 30, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	info := NewImageInfo(0, src)
	info.PreviewPaths = []string{src}

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatalf("dirPreviewThumbnail: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if w, h := img.Bounds().Dx(), img.Bounds().Dy(); w != 600 || h != 450 {
		t.Fatalf("thumbnail size = %dx%d, want 600x450", w, h)
	}

	// Icon background area (top-left) must be darkened relative to the
	// source red; a point far from the icon keeps the source color.
	ri, _, _, _ := img.At(179, 45).RGBA()
	ra, _, _, _ := img.At(500, 400).RGBA()
	if ri>>8 > 150 {
		t.Fatalf("icon area not darkened: R=%d", ri>>8)
	}
	if ra>>8 < 150 {
		t.Fatalf("non-icon area lost source color: R=%d", ra>>8)
	}
}

// The generated thumbnail is written to the disk cache under a stable key
// (content hash + "d" suffix) and served from there on the next call.
func TestDirPreviewThumbnailCache(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "a.jpg")
	writeTestJPEG(t, src, 640, 480, color.RGBA{30, 200, 30, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	info := NewImageInfo(0, src)
	info.PreviewPaths = []string{src}

	if _, err := layout.dirPreviewThumbnail(info); err != nil {
		t.Fatal(err)
	}
	var cacheFile string
	filepath.Walk(layout.config.General.ThumbnailDir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			cacheFile = p
		}
		return nil
	})
	if cacheFile == "" {
		t.Fatal("no cache file written")
	}
	if len(filepath.Base(cacheFile)) != 65 {
		t.Fatalf("cache key = %q, want 64-hex hash + \"d\" suffix", filepath.Base(cacheFile))
	}

	sentinel := []byte("cached-thumbnail")
	if err := os.WriteFile(cacheFile, sentinel, 0644); err != nil {
		t.Fatal(err)
	}
	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, sentinel) {
		t.Fatal("cache hit did not return sentinel")
	}
}

// previewIndex selects which preview is thumbnailed; out-of-range indices
// clamp to the first preview.
func TestDirPreviewThumbnailIndex(t *testing.T) {
	tmp := t.TempDir()
	srcA := filepath.Join(tmp, "a.jpg")
	srcB := filepath.Join(tmp, "b.jpg")
	writeTestJPEG(t, srcA, 640, 480, color.RGBA{200, 30, 30, 255})
	writeTestJPEG(t, srcB, 800, 800, color.RGBA{30, 30, 200, 255})

	layout := testDirLayout(filepath.Join(tmp, "cache"))
	info := NewImageInfo(0, srcA)
	info.PreviewPaths = []string{srcA, srcB}

	dataA, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	info.previewIndex = 1
	dataB, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(dataA, dataB) {
		t.Fatal("different previews produced identical thumbnails")
	}

	info.previewIndex = 99 // out of range → clamps to first
	dataC, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dataA, dataC) {
		t.Fatal("out-of-range index did not clamp to first preview")
	}
}

// Archive entries read preview members through the archive filesystem.
func TestDirPreviewThumbnailArchive(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	stdraw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{40, 200, 40, 255}}, image.Point{}, stdraw.Src)
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{"pics/one.jpg": &fstest.MapFile{Data: buf.Bytes()}}

	layout := testDirLayout(filepath.Join(t.TempDir(), "cache"))
	info := NewImageInfo(0, "pics/one.jpg")
	info.InputIsArchive = true
	info.archiveFile = fsys
	info.PreviewPaths = []string{"pics/one.jpg"}

	data, err := layout.dirPreviewThumbnail(info)
	if err != nil {
		t.Fatalf("archive preview: %v", err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if w := dec.Bounds().Dx(); w != 600 {
		t.Fatalf("thumbnail width = %d, want 600", w)
	}
}

// drawFolderIcon darkens the badge background and paints a light folder
// glyph on top of it.
func TestDrawFolderIcon(t *testing.T) {
	size := 96

	white := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	stdraw.Draw(white, white.Bounds(), &image.Uniform{color.NRGBA{255, 255, 255, 255}}, image.Point{}, stdraw.Src)
	drawFolderIcon(white, 8, 8, size)
	// Inside the rounded-square badge but outside the glyph: darkened.
	r, g, b, _ := white.At(8+size-10, 8+10).RGBA()
	if r>>8 > 200 || g>>8 > 200 || b>>8 > 200 {
		t.Fatalf("badge background not drawn: %d,%d,%d", r>>8, g>>8, b>>8)
	}

	black := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	drawFolderIcon(black, 8, 8, size)
	// Folder body center: lightened by the white glyph.
	r2, _, _, _ := black.At(8+size/2, 8+52).RGBA()
	if r2>>8 < 100 {
		t.Fatalf("folder glyph not drawn: R=%d", r2>>8)
	}
}

func TestToRGBA(t *testing.T) {
	if toRGBA(nil) != nil {
		t.Fatal("nil should stay nil")
	}
	rgba := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if toRGBA(rgba) != rgba {
		t.Fatal("RGBA should pass through unchanged")
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	nrgba.SetNRGBA(2, 2, color.NRGBA{255, 0, 0, 255})
	out := toRGBA(nrgba)
	r, g, b, a := out.At(2, 2).RGBA()
	if r>>8 != 255 || g>>8 != 0 || b>>8 != 0 || a>>8 != 255 {
		t.Fatalf("NRGBA conversion produced %d,%d,%d,%d", r>>8, g>>8, b>>8, a>>8)
	}
	// YCbCr (what JPEG decoding produces) must convert without panic.
	if toRGBA(image.NewYCbCr(image.Rect(0, 0, 4, 4), image.YCbCrSubsampleRatio420)) == nil {
		t.Fatal("YCbCr conversion returned nil")
	}
}

// A cached ImageView whose bitmap was released (showGallery frees it) must
// be reloaded when the view is shown again — regression: previously seen
// images came back blank.
func TestLoadImageToCacheReloadsReleasedView(t *testing.T) {
	test.NewApp()
	win := test.NewWindow(nil)

	v := &Gallery{
		cache:    make(map[string]*ImageView),
		window:   win,
		platform: NewPlatform(),
	}
	info := NewImageInfoCustomReader(0, memReader{path: "p", data: testJPEGBytes(t, 64, 48, color.RGBA{10, 20, 30, 255})})
	info.Path = "p"

	iv1 := v.LoadImageToCache(info)
	if iv1.fyneImage == nil || iv1.fyneImage.Image == nil {
		t.Fatal("initial load did not produce an image")
	}

	// Simulate showGallery's release of the bitmap.
	iv1.fullImage = nil
	iv1.fyneImage.Image = nil

	iv2 := v.LoadImageToCache(info)
	if iv2 != iv1 {
		t.Fatal("expected the cached view instance")
	}
	if iv2.fyneImage.Image == nil {
		t.Fatal("released view was not reloaded on cache hit")
	}
}

// NewImageView on an undecodable image must fall back to the placeholder so
// fyneImage is never nil (regression: renderer Layout panicked on
// fyneImage.Resize after a failed decode).
func TestNewImageViewBadImage(t *testing.T) {
	info := NewImageInfoCustomReader(0, errReader{})
	iv := NewImageView(info, fyne.NewSize(100, 100), true, nil, nil, NewPlatform())
	if iv.fyneImage == nil {
		t.Fatal("fyneImage is nil after failed decode")
	}
	iv.CreateRenderer().(*ImageViewRenderer).Layout(fyne.NewSize(100, 100))
}
