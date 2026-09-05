package gallery

// Extension Interfaces — Gallery Library API
//
// This file documents the stable extension contract for the gallery package.
// Both imgview and tie-view depend on these interfaces to customize gallery
// behavior without modifying the library itself. This is the "two apps, one
// library" seam.
//
// The gallery package provides the rendering engine (justified row layout, tile
// grid, single-image viewer, thumbnailing, pagination). Applications extend it
// by implementing these interfaces to supply content from different sources
// (local filesystem, tie network, archives, etc.).

// ═══════════════════════════════════════════════════════════════════════════
// CORE EXTENSION INTERFACE
// ═══════════════════════════════════════════════════════════════════════════

// CustomReader is the core extension interface. Applications implement this to
// supply image content from any source: tie network blobs, archive members,
// Android content URIs, remote HTTP, etc.
//
// The gallery calls GetReader when it needs to decode an image (for thumbnail
// generation or full-resolution display). Path returns a stable identifier used
// for caching and display; it need not be a filesystem path (e.g. tie uses
// content hashes, archives use "archive.zip/image.jpg").
//
// Example implementations:
//   - tieReader (cmd/tie-view/tie.go): fetches from tie filehost
//   - uriReader (cmd/imgview/main.go): reads Android content:// URIs
//   - ImageInfo (gallery/imageinfo.go): reads local files (default behavior)
//
// Lifecycle: Gallery.ReadCustom accepts []CustomReader and populates the grid
// with one tile per reader. Each reader's GetReader is called on-demand by
// thumbnail workers (phase: gallery load) and by LoadImage (phase: single-image
// view).
//
// Thread safety: GetReader may be called concurrently by multiple thumbnail
// workers. Implementations must be safe for concurrent reads or must serialize
// internally.
//
// For interface definition, see gallery/gallery.go:617

// ═══════════════════════════════════════════════════════════════════════════
// OPTIONAL BEHAVIOR INTERFACES
// ═══════════════════════════════════════════════════════════════════════════

// Openable is an optional CustomReader extension for entries that are not
// viewable images (e.g. directories, collections). When a reader implements
// Openable, tapping its tile calls Open() instead of trying to display it as
// an image.
//
// Example: tieDirReader (cmd/tie-view/tie.go) navigates to the directory in the
// virtual filesystem tree when opened.
//
// For interface definition, see gallery/gallery.go:625

// VideoFile is an optional CustomReader extension marking video entries. When
// IsVideo() returns true, the gallery:
//   - Shows a video-placeholder thumbnail with a play icon
//   - Skips image decoding (avoids "unsupported format" errors)
//   - Lets the tile click handler open a video player instead
//
// The gallery itself does not play videos; the application's tile-onclick
// handler (passed to NewGallery) is responsible for launching the player.
//
// Example: tieReader implements VideoFile when the tie blob's MIME type is
// video/* (cmd/tie-view/tie.go).
//
// For interface definition, see gallery/gallery.go:633

// VideoStreamer is an optional CustomReader extension for video entries that
// can be played directly from an HTTP URL without downloading the full blob.
// Used for:
//   - Generating video thumbnails via libmpv (avoids full download)
//   - Streaming playback in the video player
//
// When implemented, GetThumbnail passes StreamURL to the libmpv frame extractor
// instead of reading the full content through GetReader.
//
// Example: tieReader returns the filehost HTTP URL when available
// (cmd/tie-view/tie.go).
//
// For interface definition, see gallery/gallery.go:641

// DimensionProvider is an optional CustomReader extension for entries whose
// original pixel dimensions are known before fetching the image blob. When
// implemented, Gallery.ReadCustom pre-populates ImageInfo.Width and Height so
// placeholder tiles have the correct aspect ratio from the first layout pass.
// This prevents layout reflow as thumbnails load.
//
// Example: tieReader parses the "WxH" dimension string stored in tie metadata
// (cmd/tie-view/tie.go).
//
// For interface definition, see gallery/gallery.go:650

// ═══════════════════════════════════════════════════════════════════════════
// THUMBNAILING EXTENSION
// ═══════════════════════════════════════════════════════════════════════════

// Thumbnailer supplies scaled thumbnails for gallery items. When the Gallery's
// Thumbnailer field is non-nil, the gallery calls GetThumbnail instead of
// generating thumbnails locally (via disk cache in ThumbnailDir).
//
// The returned ReadSeeker should be a pre-scaled JPEG at ~2× the tile width
// (see Config.TileWidth). The gallery decodes it and renders it directly; no
// additional scaling occurs.
//
// Example: filehostThumbnailer (cmd/tie-view/thumbnailer.go) fetches cached
// thumbnails from tie or generates them on-demand and persists them back to
// the tie metadata store.
//
// Lifecycle: GetThumbnail is called by thumbnail worker goroutines during
// gallery load. Multiple workers may call it concurrently for different items.
//
// For interface definition, see gallery/tilelayout.go:44

// ═══════════════════════════════════════════════════════════════════════════
// CALLBACK EXTENSION POINTS
// ═══════════════════════════════════════════════════════════════════════════

// The Gallery struct exposes several callback fields that applications set to
// customize behavior:
//
// OnImageChange func(*ImageInfo)
//   Called after ChangeImage displays a new image. Used by:
//   - Both mains: focus the image view on desktop (via FocusImageViewOnDesktop)
//   - tie-view: overlay the tag panel on the image view
//
// OnTapped func()
//   Called when the user taps the image view (desktop: single click). Used by:
//   - tie-view: toggle the tag panel overlay
//
// OnSwipeUp func()
//   Called when the user swipes upward on the image view. Preferred over
//   OnTapped on mobile to avoid conflicts with pinch-zoom. Used by:
//   - tie-view: toggle the tag panel overlay (mobile only)
//
// OnDoubleTapped func()
//   Called on double-tap/double-click of the image view. Default behavior:
//   toggle fullscreen (wired in Gallery.LoadImageToCache).
//
// OnTappedSecondary func()
//   Called on right-click (desktop) or long-press (mobile) of the image view.
//   Not currently used.
//
// OnTileSecondaryTapped func(*Tile)
//   Called on right-click/long-press of a gallery tile. Used by:
//   - tie-view: show de-import confirmation dialog
//
// Sidebar fyne.CanvasObject
//   When non-nil, shown left of the gallery grid. Used by:
//   - tie-view: tag filter sidebar with co-tag refinement
//
// MenuItems func() []*fyne.MenuItem
//   When non-nil, returns extra items appended to the gallery's ☰ popup menu
//   (both the bottom-bar button and the floating image-view button). Called
//   each time the menu opens, so labels can reflect live state. Used by:
//   - tie-view: "Show/Hide hidden directories" toggle for the files tree

// ═══════════════════════════════════════════════════════════════════════════
// STABILITY GUARANTEE
// ═══════════════════════════════════════════════════════════════════════════

// The interfaces documented in this file are the stable extension contract.
// Applications (imgview, tie-view) depend on them and should not need to change
// when gallery internals are refactored.
//
// Additions to these interfaces (new optional methods) are backward-compatible.
// Changes to existing method signatures are breaking changes and should be
// avoided or versioned.
//
// Internal gallery types (TileLayout, ImageView, Region, etc.) are not part of
// the stable API. Applications should not depend on their internals.
