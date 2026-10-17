package gallery

import (
	"image"
	"io/fs"
	"sync"
)

// ImageInfo is the per-item data model for gallery entries (images, videos,
// directories, or archives). It holds file metadata, display callbacks, and
// optional dimension information for stable placeholder layout.
//
// Threading. An entry outlives the goroutine that created it: it is shared
// between the UI goroutine and the gallery's loader workers for as long as its
// page is on screen. The fields declared directly on the struct are the
// immutable core — written once while the listing is built (ReadCustom,
// ReadImageDir, the NewImageInfo constructors) and never written again. The
// mutable state lives in the block below behind stateMu and is reachable only
// through the accessors: previewIndex, the preview readers, the dimensions,
// the scaled flag and the retry counter are each written by a loader worker
// and read by the UI goroutine, or the other way round, and there is no
// ordering between those goroutines.
type ImageInfo struct {
	// ═════════════════ immutable core: set once, then read-only ═══════════
	Path     string
	FullPath string // Used to get path of zipFile
	// DirPath is the primary sort key: the directory or container that this
	// entry logically belongs to (subdir path, archive path, or parent dir).
	DirPath string
	// DisplayName is the text shown below the tile (dirname for dirs, filename otherwise).
	DisplayName string
	// Subtitle, when non-empty, is shown on a second line below DisplayName in
	// a lighter (non-bold) style; DisplayName is then rendered bold. Used for
	// e.g. an album title over its artist. Wired from a Subtitler CustomReader.
	Subtitle string
	// PreviewPaths holds the image paths shown on a directory/archive tile:
	// absolute file paths for directories, member paths (read via archiveFile)
	// for archives. The tile displays PreviewPaths[PreviewIndex()]; swiping
	// horizontally on the tile cycles the index.
	PreviewPaths      []string
	ShowArchive       bool
	CustomReader      CustomReader
	OnTapped          func()
	OnDoubleTapped    func()
	OnTappedSecondary func()
	// OnSwipeUp, when non-nil, is called when the user performs an upward
	// swipe gesture on the image view (mobile only). Used instead of OnTapped
	// on mobile to avoid conflicts with normal image interaction.
	OnSwipeUp func()
	// OnOpen, when non-nil, replaces the default image display when the
	// entry is opened (tile tap, next/prev navigation) — e.g. to browse
	// into a directory the entry represents. Wired automatically from
	// CustomReader when it implements Openable.
	OnOpen func()

	archiveName string
	archiveFile fs.FS
	order       int

	InputIsArchive bool
	InputIsDir     bool
	InputIsReader  bool
	InputIsVideo   bool
	IsZoomable     bool
	IsDraggable    bool

	// ═════════ mutable: reach only through the accessors below ════════════
	stateMu sync.Mutex
	// previewIndex is the preview index currently shown on the tile. For
	// video tiles it is the frame number (0..videoPreviewFrames-1).
	previewIndex int
	// previewReaders is the CustomReader-backed counterpart of PreviewPaths,
	// populated lazily from a PreviewProvider (e.g. a tie directory or
	// archive) by a loader worker.
	previewReaders []CustomReader
	// width and height are the pixel dimensions of the original image. When
	// non-zero they are used for the placeholder tile's aspect ratio before
	// the thumbnail blob has been fetched, preventing layout reflow.
	width  int
	height int
	// thumbScaled records that the thumbnail on screen is a downscaled
	// version of the original, so the image view knows it must fetch the
	// full-size image before zooming in.
	thumbScaled bool
	// attempts counts thumbnail-load retries (bounded by
	// maxTileLoadAttempts); placeTiles resets it when (re-)enqueueing the
	// item so a revisited page gets a fresh budget.
	attempts int

	// prefetched holds a full-size bitmap decoded ahead of the image view
	// being built (the next-image prefetch). It has its own mutex because
	// decoding is slow and must not block the accessors above.
	prefetchMu     sync.Mutex
	prefetched     image.Image
	prefetchFormat string
}

func NewImageInfo(order int, path string) *ImageInfo {
	return &ImageInfo{
		Path:        path,
		IsDraggable: true,
		IsZoomable:  true,
		order:       order,
	}
}

func NewImageInfoCustomReader(order int, r CustomReader) *ImageInfo {
	return &ImageInfo{
		InputIsReader: true,
		CustomReader:  r,
		IsDraggable:   true,
		IsZoomable:    true,
		order:         order,
	}
}

// --- accessors for the mutable state ---------------------------------------

// Dimensions returns the known pixel size of the original image, or (0, 0)
// when it is not known yet.
func (info *ImageInfo) Dimensions() (int, int) {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	return info.width, info.height
}

// SetDimensions records the original image size, so a placeholder tile can be
// given its final aspect ratio before the thumbnail arrives.
func (info *ImageInfo) SetDimensions(w, h int) {
	info.stateMu.Lock()
	info.width, info.height = w, h
	info.stateMu.Unlock()
}

// ThumbnailIsScaled reports whether the thumbnail currently shown is a
// downscale of the original rather than its pixels.
func (info *ImageInfo) ThumbnailIsScaled() bool {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	return info.thumbScaled
}

// SetThumbnailScaled records that the thumbnail on screen was downscaled.
func (info *ImageInfo) SetThumbnailScaled(v bool) {
	info.stateMu.Lock()
	info.thumbScaled = v
	info.stateMu.Unlock()
}

// PreviewReaders returns the lazily resolved preview sources of a directory or
// archive entry. The result is a snapshot: a worker may install a different
// slice afterwards.
func (info *ImageInfo) PreviewReaders() []CustomReader {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	return info.previewReaders
}

// SetPreviewReaders installs the previews resolved by a PreviewProvider.
func (info *ImageInfo) SetPreviewReaders(readers []CustomReader) {
	info.stateMu.Lock()
	info.previewReaders = readers
	info.stateMu.Unlock()
}

// PreviewIndex is the preview currently shown on the tile (for a video tile,
// the frame number).
func (info *ImageInfo) PreviewIndex() int {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	return info.previewIndex
}

// SetPreviewIndex selects the preview shown on the tile.
func (info *ImageInfo) SetPreviewIndex(i int) {
	info.stateMu.Lock()
	info.previewIndex = i
	info.stateMu.Unlock()
}

// AdvancePreview moves the preview index by delta, wrapping inside [0,n), and
// returns the new index. The read and the write are one step: a swipe landing
// while a loader worker reads the index must not see a torn update.
func (info *ImageInfo) AdvancePreview(delta, n int) int {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	if n <= 0 {
		return info.previewIndex
	}
	info.previewIndex = ((info.previewIndex+delta)%n + n) % n
	return info.previewIndex
}

// loadAttempts returns the retry count for the current placement.
func (info *ImageInfo) loadAttempts() int {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	return info.attempts
}

// bumpLoadAttempts increments and returns the retry count.
func (info *ImageInfo) bumpLoadAttempts() int {
	info.stateMu.Lock()
	defer info.stateMu.Unlock()
	info.attempts++
	return info.attempts
}

// resetLoadAttempts gives the entry a fresh retry budget, which placeTiles does
// for every item it enqueues.
func (info *ImageInfo) resetLoadAttempts() {
	info.stateMu.Lock()
	info.attempts = 0
	info.stateMu.Unlock()
}

// --- derived queries -------------------------------------------------------

// HasPreviews reports whether the tile gets a swipe overlay for cycling
// preview thumbnails: directory/archive entries with known previews, entries
// whose CustomReader can provide them lazily (PreviewProvider), and video
// tiles (which cycle through extracted frames).
func (info *ImageInfo) HasPreviews() bool {
	if info.InputIsVideo || len(info.PreviewPaths) > 0 || len(info.PreviewReaders()) > 0 {
		return true
	}
	_, ok := info.CustomReader.(PreviewProvider)
	return ok
}

// PreviewCount reports how many thumbnails a horizontal swipe can cycle
// through: the fixed frame count for videos, or the number of preview
// paths/readers for directory/archive entries. 0 means no cycling.
func (info *ImageInfo) PreviewCount() int {
	if info.InputIsVideo {
		return videoPreviewFrames
	}
	if len(info.PreviewPaths) > 0 {
		return len(info.PreviewPaths)
	}
	return len(info.PreviewReaders())
}

// decodedImage returns the entry's decoded bitmap, taking it from the prefetch
// slot when the next-image prefetch has already read it. Decoding touches no
// Fyne object, so this is safe on a background goroutine; attaching the result
// to an ImageView is not.
func (info *ImageInfo) decodedImage() (image.Image, string, error) {
	info.prefetchMu.Lock()
	img, format := info.prefetched, info.prefetchFormat
	info.prefetched, info.prefetchFormat = nil, ""
	info.prefetchMu.Unlock()
	if img != nil {
		return img, format, nil
	}
	reader, err := info.GetReader()
	if err != nil {
		return nil, "", err
	}
	return Decode(reader)
}

// setPrefetched parks a decoded bitmap for the next decodedImage call.
func (info *ImageInfo) setPrefetched(img image.Image, format string) {
	info.prefetchMu.Lock()
	info.prefetched, info.prefetchFormat = img, format
	info.prefetchMu.Unlock()
}

// orderIndex is the entry's position in its listing; read-only after the
// listing is built, exposed for tests that assert placement bookkeeping.
func (info *ImageInfo) orderIndex() int { return info.order }
