// Where a tile's thumbnail comes from: the filehost Thumbnailer, the local disk
// cache and its sharded layout, directory/archive previews, and video frame
// previews.
package gallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	stdraw "image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strconv"

	_ "embed"

	"github.com/uidbz/tie-gui/mpvplayer"
)

// bitmapFromCache returns a decoded thumbnail already in the cache.
func (layout *TileLayout) bitmapFromCache(path string) (*image.RGBA, bool) {
	return layout.thumbCache.get(path)
}

// bitmapToCache stores a decoded thumbnail for later reuse. The bitmap, not
// the tile, is what is shared: a canvas object can only have one parent, so
// reusing a *Tile for two grid slots (same content hash, two names) made the
// layout move one object twice.
func (layout *TileLayout) bitmapToCache(path string, img *image.RGBA) {
	layout.thumbCache.put(path, img)
}

func (layout *TileLayout) GetThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	// Video files: extract a frame thumbnail for both local and
	// network-backed entries (the reader is seekable in both cases).
	if info.InputIsVideo {
		return layout.videoThumbnail(info)
	}

	// Directory and archive tiles: thumbnail of the currently selected
	// preview image (previewIndex, changed by swiping the tile) with a
	// semi-transparent folder icon overlay. Entries whose CustomReader can
	// supply previews (PreviewProvider) enter this branch too; the preview
	// list is resolved lazily inside dirPreviewThumbnail. On failure the
	// code falls through to the Thumbnailer (e.g. folder icon) or the
	// generic path instead of leaving the tile on the placeholder.
	_, hasPreviewProvider := info.CustomReader.(PreviewProvider)
	if len(info.PreviewPaths) > 0 || len(info.PreviewReaders()) > 0 || hasPreviewProvider {
		data, err := layout.dirPreviewThumbnail(info)
		if err == nil {
			info.SetThumbnailScaled(true)
			return bytes.NewReader(data), nil
		}
	}

	// A custom Thumbnailer (e.g. one backed by network storage) takes
	// precedence over the local thumbnail directory, but only for
	// reader-backed entries. Plain local files (no CustomReader) always use
	// the local disk cache — tie-view browsing a local directory keeps
	// imgview's cache, whose content-hash keys are shared with tie.
	if layout.thumbnailer != nil && info.CustomReader != nil {
		return layout.thumbnailer.GetThumbnail(info)
	}

	var reader io.ReadSeeker
	r, err := info.GetReader()
	if err != nil {
		return nil, err
	}
	r.Seek(0, io.SeekStart)
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	hash, err := contentHash(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	reader = bytes.NewReader(b)
	if len(hash) != 64 {
		return nil, errors.New("Invalid hash: " + hash)
	}
	thumbnail := shardedPath(layout.config.General.ThumbnailDir, hash, "")
	thumbnailDir := filepath.Dir(thumbnail)
	if _, err := os.Stat(thumbnail); err == nil && !layout.refreshThumbs {
		reader, err = os.Open(thumbnail)
		if err != nil {
			return nil, err
		}
		info.SetThumbnailScaled(true)
		return reader, nil
	} else {
		err := os.MkdirAll(thumbnailDir, 0755)
		if err != nil {
			// Cache dir creation failed; thumbnail will not be cached on disk
		} else {
			decoded, _, err := Decode(reader)
			if err != nil {
				return nil, err
			}
			tileWidth := int(layout.config.General.TileWidth)
			scaled := ScaleImage(decoded, tileWidth*2)
			decoded = nil
			buf := &bytes.Buffer{}
			err = jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90})
			if err == nil {
				// Write to disk cache; ignore errors (thumbnail still returned in-memory)
				os.WriteFile(thumbnail, buf.Bytes(), 0755)
			}
			info.SetThumbnailScaled(true)

			return bytes.NewReader(buf.Bytes()), nil
		}
	}

	return info.GetReader()
}

// dirPreviewThumbnail generates (or retrieves from disk cache) the thumbnail
// for a directory or archive tile: the preview image at info.PreviewIndex(),
// scaled to 2×tileWidth wide, with a semi-transparent folder icon overlaid in
// the top-left corner (the same way video tiles get a play icon).
//
// Previews come from PreviewPaths (local directories: file paths; local
// archives: member paths read via archiveFile) or from PreviewReaders
// (CustomReader-backed entries such as tie directories and archive blobs).
// The disk cache key is the preview's content hash with a "d" suffix so the
// overlaid thumbnail never collides with the same image's plain thumbnail.
func (layout *TileLayout) dirPreviewThumbnail(info *ImageInfo) ([]byte, error) {
	tileWidth := int(layout.config.General.TileWidth)
	var cacheKey string
	var scaled image.Image

	if len(info.PreviewPaths) > 0 {
		idx := info.PreviewIndex()
		if idx < 0 || idx >= len(info.PreviewPaths) {
			idx = 0
		}
		previewPath := info.PreviewPaths[idx]

		// Read the preview image content: from the archive for archive
		// entries, from disk for directory entries.
		var b []byte
		if info.InputIsArchive {
			if info.archiveFile == nil {
				return nil, errors.New("archive not readable")
			}
			f, err := info.archiveFile.Open(previewPath)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			b, err = io.ReadAll(f)
			if err != nil {
				return nil, err
			}
		} else {
			var err error
			b, err = os.ReadFile(previewPath)
			if err != nil {
				return nil, err
			}
		}

		hash, err := contentHash(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if len(hash) != 64 {
			return nil, errors.New("Invalid hash: " + hash)
		}
		cacheKey = hash
		if data, ok := layout.dirPreviewCache(cacheKey); ok {
			return data, nil
		}
		decoded, _, err := Decode(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		scaled = ScaleImage(decoded, tileWidth*2)
	} else {
		// Cover fast path: entries with a ready-made cover thumbnail
		// (CoverProvider, e.g. a server-cached tie archive cover) serve the
		// tile's initial view WITHOUT enumerating the collection — no
		// archive download, no directory query.
		if cp, ok := info.CustomReader.(CoverProvider); ok &&
			info.PreviewIndex() == 0 && len(info.PreviewReaders()) == 0 {
			cacheKey = readerCacheKey(info.CustomReader.Path())
			if data, ok := layout.dirPreviewCache(cacheKey); ok {
				return data, nil
			}
			if rs, err := cp.CoverThumbnail(); err == nil {
				decoded, _, derr := Decode(rs)
				if derr != nil {
					return nil, derr
				}
				return layout.finishDirPreview(cacheKey, decoded)
			}
			// Cover unavailable: fall through to the preview-readers path;
			// the generated first preview is stored as the cover below.
		}

		// Lazily resolve the preview readers (PreviewProvider). This is
		// where collection enumeration happens — one cheap query for tie
		// directories, a full blob download for tie archives.
		if len(info.PreviewReaders()) == 0 {
			if pp, ok := info.CustomReader.(PreviewProvider); ok {
				if pr, err := pp.Previews(); err == nil {
					info.SetPreviewReaders(pr)
				}
			}
			if len(info.PreviewReaders()) == 0 {
				return nil, errors.New("no previews available")
			}
		}
		idx := info.PreviewIndex()
		if idx < 0 || idx >= len(info.PreviewReaders()) {
			idx = 0
		}
		r := info.PreviewReaders()[idx]
		cacheKey = readerCacheKey(r.Path())
		if data, ok := layout.dirPreviewCache(cacheKey); ok {
			return data, nil
		}

		// plainJPEG keeps the pre-badge thumbnail bytes so a CoverProvider
		// can store them as the entry's cover for next time.
		var plainJPEG []byte
		if layout.thumbnailer != nil {
			// Let the app's Thumbnailer (e.g. the tie filehost cache)
			// supply the already-scaled preview thumbnail.
			child := NewImageInfoCustomReader(0, r)
			child.Path = r.Path()
			rs, err := layout.thumbnailer.GetThumbnail(child)
			if err != nil {
				return nil, err
			}
			plainJPEG, err = io.ReadAll(rs)
			if err != nil {
				return nil, err
			}
			decoded, _, err := Decode(bytes.NewReader(plainJPEG))
			if err != nil {
				return nil, err
			}
			scaled = decoded
		} else {
			rs, err := r.GetReader()
			if err != nil {
				return nil, err
			}
			decoded, _, err := Decode(rs)
			if err != nil {
				return nil, err
			}
			scaled = ScaleImage(decoded, tileWidth*2)
			if _, ok := info.CustomReader.(CoverProvider); ok && info.PreviewIndex() == 0 {
				buf := &bytes.Buffer{}
				if err := jpeg.Encode(buf, scaled, &jpeg.Options{Quality: 90}); err == nil {
					plainJPEG = buf.Bytes()
				}
			}
		}

		if cp, ok := info.CustomReader.(CoverProvider); ok &&
			info.PreviewIndex() == 0 && plainJPEG != nil {
			cp.StoreCoverThumbnail(plainJPEG)
		}
	}

	return layout.finishDirPreview(cacheKey, scaled)
}

// readerCacheKey derives a 64-hex disk-cache key from a reader path: tie
// readers already use content hashes; anything else is hashed by name.
func readerCacheKey(path string) string {
	if len(path) == 64 {
		return path
	}
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

// finishDirPreview badges a scaled preview image with the folder icon,
// encodes it as JPEG, and writes it to the disk cache under cacheKey.
func (layout *TileLayout) finishDirPreview(cacheKey string, scaled image.Image) ([]byte, error) {
	// Convert to NRGBA for the icon overlay.
	result := image.NewNRGBA(scaled.Bounds())
	stdraw.Draw(result, result.Bounds(), scaled, image.Point{}, stdraw.Src)

	iconPx := int(layout.config.General.TileWidth) * 2 / 4 // ~25 % of image width; halved at display scale
	if iconPx < 24 {
		iconPx = 24
	}
	drawFolderIcon(result, iconPx/4, iconPx/4, iconPx)

	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, result, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	cachePath := layout.dirPreviewCachePath(cacheKey)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
		os.WriteFile(cachePath, buf.Bytes(), 0644)
	}
	return buf.Bytes(), nil
}

// dirPreviewCachePath returns the disk cache path for a directory/archive
// preview thumbnail: the 64-hex cache key split into directory levels, with
// a "d" suffix on the filename.
func (layout *TileLayout) dirPreviewCachePath(cacheKey string) string {
	return shardedPath(layout.config.General.ThumbnailDir, cacheKey, "d")
}

// dirPreviewCache returns the cached preview thumbnail for cacheKey.
func (layout *TileLayout) dirPreviewCache(cacheKey string) ([]byte, bool) {
	if layout.refreshThumbs {
		return nil, false
	}
	data, err := os.ReadFile(layout.dirPreviewCachePath(cacheKey))
	return data, err == nil
}

// videoPreviewFrames is the number of frame thumbnails a video tile offers
// when swiped horizontally. Frames are spread across the duration by seek
// percent, so any video length works without probing it first.
const videoPreviewFrames = 10

// videoThumbnail generates (or retrieves from disk cache) a thumbnail for a
// video entry (local file or network-backed CustomReader). It extracts the
// frame at info.PreviewIndex() (changed by swiping the tile), scales it to
// 2×tileWidth wide, and overlays a circular play icon in the top-left corner.
// On any failure it falls back to the loading placeholder.
func (layout *TileLayout) videoThumbnail(info *ImageInfo) (io.ReadSeeker, error) {
	tileW := int(layout.config.General.TileWidth)

	frameIdx := info.PreviewIndex()
	if frameIdx < 0 || frameIdx >= videoPreviewFrames {
		frameIdx = 0
	}

	// Check disk cache before running mpv.
	cachePath := layout.videoThumbnailCachePath(info.Path, frameIdx)
	if data, err := os.ReadFile(cachePath); err == nil && !layout.refreshThumbs {
		info.SetThumbnailScaled(true)
		return bytes.NewReader(data), nil
	}

	// Extract a frame using libmpv's software renderer. Frame k seeks to
	// (k+1)/(frames+1) of the duration, skipping the very start and end
	// (often black frames). For network-backed entries that expose a stream
	// URL, pass the URL directly so libmpv streams without downloading.
	percent := float64(frameIdx+1) / float64(videoPreviewFrames+1) * 100
	var frame image.Image
	tileW2 := tileW * 2
	if info.InputIsReader {
		if vs, ok := info.CustomReader.(VideoStreamer); ok && vs.StreamURL() != "" {
			frame = mpvplayer.ExtractFramePercent(vs.StreamURL(), tileW2, tileW2, percent)
		} else if r, err := info.GetReader(); err == nil {
			frame = mpvplayer.ExtractFrameFromReaderPercent(r, tileW2, tileW2, percent)
		}
	} else {
		frame = mpvplayer.ExtractFramePercent(info.Path, tileW2, tileW2, percent)
	}
	if frame == nil {
		return bytes.NewReader(loading), nil
	}

	// Scale to 2×tileWidth wide, keeping aspect ratio.
	scaled := ScaleImage(frame, tileW*2)

	// Convert to NRGBA for pixel-level drawing.
	result := image.NewNRGBA(scaled.Bounds())
	stdraw.Draw(result, result.Bounds(), scaled, image.Point{}, stdraw.Src)

	// Overlay play icon in top-left corner.
	iconPx := tileW * 2 / 4 // ~25 % of image width; halved at display scale
	if iconPx < 24 {
		iconPx = 24
	}
	drawVideoPlayIcon(result, iconPx/4, iconPx/4, iconPx)

	// Encode and write to cache.
	buf := &bytes.Buffer{}
	jpeg.Encode(buf, result, &jpeg.Options{Quality: 90})
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
		os.WriteFile(cachePath, buf.Bytes(), 0644)
	}

	info.SetThumbnailScaled(true)
	return bytes.NewReader(buf.Bytes()), nil
}

// videoThumbnailCachePath returns the path under ThumbnailDir where the video
// frame thumbnail is cached, using a SHA-256 hash of the video file path as
// the key (avoiding reading the full video file just for hashing). Frame 0
// keeps the historic suffix-free name so existing caches stay valid; other
// frames get a "-N" suffix.
func (layout *TileLayout) videoThumbnailCachePath(videoPath string, frame int) string {
	sum := sha256.Sum256([]byte(videoPath))
	hash := "v" + hex.EncodeToString(sum[:])
	if frame > 0 {
		hash += "-" + strconv.Itoa(frame)
	}
	dir := layout.config.General.ThumbnailDir
	for i := 0; i < 3; i++ {
		// skip the "v" prefix when indexing into the hash for dir levels
		dir = filepath.Join(dir, hash[i*2+1:i*2+3])
	}
	return filepath.Join(dir, hash)
}

// blendNRGBA alpha-blends c onto the pixel at (x, y) if inside dst's bounds.
func blendNRGBA(dst *image.NRGBA, x, y int, c color.NRGBA) {
	b := dst.Bounds()
	if x >= b.Min.X && y >= b.Min.Y && x < b.Max.X && y < b.Max.Y {
		src := dst.NRGBAAt(x, y)
		a := float32(c.A) / 255
		dst.SetNRGBA(x, y, color.NRGBA{
			R: uint8(float32(c.R)*a + float32(src.R)*(1-a)),
			G: uint8(float32(c.G)*a + float32(src.G)*(1-a)),
			B: uint8(float32(c.B)*a + float32(src.B)*(1-a)),
			A: 255,
		})
	}
}

// drawVideoPlayIcon overlays a semi-transparent circular play button at
// (x0, y0) with the given pixel size onto dst.
func drawVideoPlayIcon(dst *image.NRGBA, x0, y0, size int) {
	radius := size / 2
	cx := x0 + radius
	cy := y0 + radius
	bg := color.NRGBA{0, 0, 0, 160}
	fg := color.NRGBA{255, 255, 255, 230}

	setPixel := func(x, y int, c color.NRGBA) {
		blendNRGBA(dst, x, y, c)
	}

	// Semi-transparent circle background.
	r2 := radius * radius
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= r2 {
				setPixel(cx+dx, cy+dy, bg)
			}
		}
	}

	// Right-pointing triangle (play symbol).
	pad := size / 5
	tLeft := x0 + pad
	tRight := x0 + size - pad
	tHalfH := radius - pad
	for y := cy - tHalfH; y <= cy+tHalfH; y++ {
		dy := y - cy
		if dy < 0 {
			dy = -dy
		}
		t := float32(dy) / float32(tHalfH)
		xLeft := tLeft + int(float32(tRight-tLeft)*t)
		for x := xLeft; x <= tRight; x++ {
			setPixel(x, y, fg)
		}
	}
}

// drawFolderIcon overlays a semi-transparent folder badge at (x0, y0) with
// the given pixel size onto dst. It marks directory and archive tiles the
// same way drawVideoPlayIcon marks video tiles.
func drawFolderIcon(dst *image.NRGBA, x0, y0, size int) {
	bg := color.NRGBA{0, 0, 0, 160}
	fg := color.NRGBA{255, 255, 255, 230}

	// Semi-transparent rounded-square background: inside each corner square,
	// keep only pixels within the corner circle.
	corner := size / 6
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			cx, cy := -1, -1
			switch {
			case x < corner && y < corner:
				cx, cy = corner, corner
			case x >= size-corner && y < corner:
				cx, cy = size-corner-1, corner
			case x < corner && y >= size-corner:
				cx, cy = corner, size-corner-1
			case x >= size-corner && y >= size-corner:
				cx, cy = size-corner-1, size-corner-1
			}
			if cx >= 0 {
				dx, dy := x-cx, y-cy
				if dx*dx+dy*dy > corner*corner {
					continue
				}
			}
			blendNRGBA(dst, x0+x, y0+y, bg)
		}
	}

	// Folder silhouette: back panel with a tab, then a slightly offset front
	// flap cut out of it (simple two-rect glyph, readable at small sizes).
	pad := size / 4
	fx0, fx1 := x0+pad, x0+size-pad
	tabH := size / 9
	bodyY0 := y0 + size/3
	bodyY1 := y0 + size - pad
	// Tab (left half of the folder top).
	for y := bodyY0 - tabH; y < bodyY0; y++ {
		for x := fx0; x <= fx0+(fx1-fx0)/2; x++ {
			blendNRGBA(dst, x, y, fg)
		}
	}
	// Body.
	for y := bodyY0; y <= bodyY1; y++ {
		for x := fx0; x <= fx1; x++ {
			blendNRGBA(dst, x, y, fg)
		}
	}
}

// The disk thumbnail cache shards a 64-hex content key into nested
// directories — <base>/<ab>/<cd>/<ef>/<gh>/<key> — so one directory does not
// end up holding every thumbnail.
//
// cacheDirLevels is the number of levels the sharding loop actually produces.
// It used to be spelled lvlDeep = 3 with a `i <= lvlDeep*dirWidth` loop, which
// walks four levels; the constant now says what happens, and the layout on disk
// is unchanged. The old package-level `max` also shadowed the builtin of the
// same name, so `max(a, b)` could not be used anywhere in this package.
const (
	cacheDirLevels = 4
	cacheDirChars  = 2
	cacheDirSpan   = cacheDirLevels * cacheDirChars
)

// shardedPath returns the cache file for key under base, with suffix appended
// to the file name. The suffix keeps two kinds of derived image — a plain
// thumbnail and a directory preview — from colliding on the same content key.
func shardedPath(base, key, suffix string) string {
	dir := base
	for i := 0; i < cacheDirSpan; i += cacheDirChars {
		dir = filepath.Join(dir, key[i:i+cacheDirChars])
	}
	return filepath.Join(dir, key+suffix)
}

// decodeThumbnailRGBA decodes a thumbnail reader into the RGBA bitmap the tile
// cache stores and the painter uploads directly.
func decodeThumbnailRGBA(reader io.ReadSeeker) (*image.RGBA, error) {
	decoded, _, err := Decode(reader)
	if err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, errors.New("decode returned no image")
	}
	return toRGBA(decoded), nil
}

// toRGBA converts img to *image.RGBA. The GL painter uploads *image.RGBA
// pixel data directly, but converts any other image type (YCbCr from JPEG
// decode, NRGBA from imaging.Resize) with a full-pixel draw.Draw on the
// paint thread at texture-upload time — calling this on a loader goroutine
// keeps that cost off the UI thread and parallelises it across workers.
func toRGBA(img image.Image) *image.RGBA {
	if img == nil {
		return nil
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}
	rgba := image.NewRGBA(img.Bounds())
	stdraw.Draw(rgba, img.Bounds(), img, img.Bounds().Min, stdraw.Src)
	return rgba
}
