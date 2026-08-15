package gallery

import "io/fs"

// ImageInfo is the per-item data model for gallery entries (images, videos,
// directories, or archives). It holds file metadata, display callbacks, and
// optional dimension information for stable placeholder layout.
type ImageInfo struct {
	Path     string
	FullPath string // Used to get path of zipFile
	// DirPath is the primary sort key: the directory or container that this
	// entry logically belongs to (subdir path, archive path, or parent dir).
	DirPath string
	// DisplayName is the text shown below the tile (dirname for dirs, filename otherwise).
	DisplayName string
	// PreviewPaths holds the image paths shown on a directory/archive tile:
	// absolute file paths for directories, member paths (read via archiveFile)
	// for archives. The tile displays PreviewPaths[previewIndex]; swiping
	// horizontally on the tile cycles the index.
	PreviewPaths []string
	// PreviewReaders is the CustomReader-backed counterpart of PreviewPaths,
	// populated lazily from a PreviewProvider (e.g. a tie directory or
	// archive): the tile shows PreviewReaders[previewIndex] as thumbnail.
	PreviewReaders []CustomReader
	// previewIndex is the preview index currently shown on the tile. For
	// video tiles it is the frame number (0..videoPreviewFrames-1).
	previewIndex      int
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

	// Width and Height are the pixel dimensions of the original image. When
	// non-zero they are used for the placeholder tile's aspect ratio before
	// the thumbnail blob has been fetched, preventing layout reflow.
	Width  int
	Height int

	archiveName       string
	archiveFile       fs.FS
	order             int
	InputIsArchive    bool
	InputIsDir        bool
	InputIsReader     bool
	InputIsVideo      bool
	IsZoomable        bool
	IsDraggable       bool
	ThumbnailIsScaled bool
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

// HasPreviews reports whether the tile gets a swipe overlay for cycling
// preview thumbnails: directory/archive entries with known previews, entries
// whose CustomReader can provide them lazily (PreviewProvider), and video
// tiles (which cycle through extracted frames).
func (info *ImageInfo) HasPreviews() bool {
	if info.InputIsVideo || len(info.PreviewPaths) > 0 || len(info.PreviewReaders) > 0 {
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
	return len(info.PreviewReaders)
}
