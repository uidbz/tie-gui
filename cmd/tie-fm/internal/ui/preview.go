// Preview mode: each file-manager pane can swap its table for the shared
// gallery grid (the same widget and thumbnail pipeline tie-view uses),
// showing thumbnail tiles for the pane's folders, images and videos.
//
// The gallery library assumes it owns the window (ChangeImage/showGallery/
// ChangePage call window.SetContent/SetTitle/SetFullScreen), so the pane is
// wrapped in a paneWindow/paneCanvas facade: content swaps are redirected to
// the pane's slot in the FileManager border layout, and title/fullscreen
// requests are dropped (tie-fm owns the real window).
package ui

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/mpvplayer"
	"github.com/uidbz/tie-gui/tiethumb"
)

// --- window/canvas facades ---

// paneCanvas is a fyne.Canvas facade scoped to one pane: SetContent replaces
// the pane's content instead of the window's, and Size reports the pane size
// (used for image-fit calculations). Focus, refresh and popups (via Overlays)
// pass through to the real window's canvas. Key handlers are deliberately
// inert: tie-fm routes window-level keys to the preview explicitly.
type paneCanvas struct {
	win  fyne.Window
	pane *fyne.Container
}

func (c *paneCanvas) Content() fyne.CanvasObject { return c.pane }
func (c *paneCanvas) SetContent(o fyne.CanvasObject) {
	c.pane.Objects = []fyne.CanvasObject{o}
	c.pane.Refresh()
}
func (c *paneCanvas) Refresh(o fyne.CanvasObject)        { c.win.Canvas().Refresh(o) }
func (c *paneCanvas) Focus(f fyne.Focusable)             { c.win.Canvas().Focus(f) }
func (c *paneCanvas) FocusNext()                         { c.win.Canvas().FocusNext() }
func (c *paneCanvas) FocusPrevious()                     { c.win.Canvas().FocusPrevious() }
func (c *paneCanvas) Unfocus()                           { c.win.Canvas().Unfocus() }
func (c *paneCanvas) Focused() fyne.Focusable            { return c.win.Canvas().Focused() }
func (c *paneCanvas) Size() fyne.Size                    { return c.pane.Size() }
func (c *paneCanvas) Scale() float32                     { return c.win.Canvas().Scale() }
func (c *paneCanvas) Overlays() fyne.OverlayStack        { return c.win.Canvas().Overlays() }
func (c *paneCanvas) OnTypedRune() func(rune)            { return nil }
func (c *paneCanvas) SetOnTypedRune(func(rune))          {}
func (c *paneCanvas) OnTypedKey() func(*fyne.KeyEvent)   { return nil }
func (c *paneCanvas) SetOnTypedKey(func(*fyne.KeyEvent)) {}
func (c *paneCanvas) AddShortcut(s fyne.Shortcut, h func(fyne.Shortcut)) {
	c.win.Canvas().AddShortcut(s, h)
}
func (c *paneCanvas) RemoveShortcut(s fyne.Shortcut) { c.win.Canvas().RemoveShortcut(s) }
func (c *paneCanvas) Capture() image.Image           { return c.win.Canvas().Capture() }
func (c *paneCanvas) PixelCoordinateForPosition(p fyne.Position) (int, int) {
	return c.win.Canvas().PixelCoordinateForPosition(p)
}
func (c *paneCanvas) InteractiveArea() (fyne.Position, fyne.Size) {
	return fyne.Position{}, c.pane.Size()
}

// paneWindow is a fyne.Window facade scoped to one pane. The gallery's
// window-level effects land in the pane (SetContent) or are dropped (title,
// fullscreen, resize): tie-fm owns the real window.
type paneWindow struct {
	win    fyne.Window
	pane   *fyne.Container
	canvas *paneCanvas
}

func newPaneWindow(win fyne.Window, pane *fyne.Container) *paneWindow {
	return &paneWindow{win: win, pane: pane, canvas: &paneCanvas{win: win, pane: pane}}
}

func (w *paneWindow) Title() string              { return w.win.Title() }
func (w *paneWindow) SetTitle(string)            {}
func (w *paneWindow) FullScreen() bool           { return false }
func (w *paneWindow) SetFullScreen(bool)         {}
func (w *paneWindow) Resize(fyne.Size)           {}
func (w *paneWindow) RequestFocus()              {}
func (w *paneWindow) FixedSize() bool            { return false }
func (w *paneWindow) SetFixedSize(bool)          {}
func (w *paneWindow) CenterOnScreen()            {}
func (w *paneWindow) Padded() bool               { return false }
func (w *paneWindow) SetPadded(bool)             {}
func (w *paneWindow) Icon() fyne.Resource        { return w.win.Icon() }
func (w *paneWindow) SetIcon(fyne.Resource)      {}
func (w *paneWindow) SetMaster()                 {}
func (w *paneWindow) MainMenu() *fyne.MainMenu   { return nil }
func (w *paneWindow) SetMainMenu(*fyne.MainMenu) {}
func (w *paneWindow) SetOnClosed(func())         {}
func (w *paneWindow) SetCloseIntercept(func())   {}
func (w *paneWindow) SetOnDropped(func(fyne.Position, []fyne.URI)) {
	// The gallery never registers a drop handler; ignore to avoid two panes
	// clobbering each other's window-wide callback.
}
func (w *paneWindow) Show()       {}
func (w *paneWindow) Hide()       {}
func (w *paneWindow) Close()      {}
func (w *paneWindow) ShowAndRun() {}
func (w *paneWindow) Content() fyne.CanvasObject {
	return w.pane
}
func (w *paneWindow) SetContent(o fyne.CanvasObject) {
	w.pane.Objects = []fyne.CanvasObject{o}
	w.pane.Refresh()
}
func (w *paneWindow) Canvas() fyne.Canvas       { return w.canvas }
func (w *paneWindow) Clipboard() fyne.Clipboard { return w.win.Clipboard() }

// --- entry readers: fs.Entry → gallery.CustomReader ---

// entryReader adapts a file fs.Entry (local, tie or mtp) to a gallery
// CustomReader. Remote content is read via the backend's Materialize (a
// cached temp copy), so thumbnails and full-size views work uniformly.
type entryReader struct {
	entry fs.Entry
	fm    *FileManager
}

// Path is the gallery's cache/identity key: the tie content hash when known
// (stable across directories and shared with tie-view's caches), else the
// full tie-fm URI.
func (r *entryReader) Path() string {
	if fs.IsTie(r.entry.Path) && r.entry.Hash != "" {
		return r.entry.Hash
	}
	return r.entry.Path
}

func (r *entryReader) DisplayName() string { return r.entry.Name }

func (r *entryReader) GetReader() (io.ReadSeeker, error) {
	if r.entry.IsDir {
		return nil, errors.New("directory is not readable content: " + r.entry.Path)
	}
	if fs.IsLocal(r.entry.Path) {
		return os.Open(strings.TrimPrefix(r.entry.Path, "file:"))
	}
	p, err := r.fm.registry.For(r.entry.Path).Materialize(r.entry)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// IsVideo implements gallery.VideoFile.
func (r *entryReader) IsVideo() bool { return isVideoName(r.entry.Name) }

// StreamURL implements gallery.VideoStreamer: tie entries stream from the
// filehost; other schemes return "" and fall back to a materialized copy.
func (r *entryReader) StreamURL() string {
	if s, ok := r.fm.registry.For(r.entry.Path).(fs.Streamer); ok {
		if url, err := s.StreamURL(r.entry); err == nil {
			return url
		}
	}
	return ""
}

// tieFileReader adds the tiethumb.ThumbReader and gallery.DimensionProvider
// behaviors: tie entries look up (and store back) the server-side
// (hash, "thumbnail", thumbHash) / (hash, "dimensions", "WxH") relations, so
// thumbnails are fetched as small blobs instead of full downloads, and tile
// aspect ratios are stable from the second viewing on.
type tieFileReader struct {
	entryReader
	thumbHash string
	dims      string
}

func (r *tieFileReader) ThumbHash() string { return r.thumbHash }

func (r *tieFileReader) SetThumbCache(thumbHash, dims string) {
	r.thumbHash = thumbHash
	if dims != "" {
		r.dims = dims
	}
}

// Dimensions implements gallery.DimensionProvider. It only serves dimensions
// already learned this session (from a thumbnail lookup or generation):
// fetching them here would run a tie Get per entry on the UI goroutine
// (ReadCustom type-asserts synchronously while building the grid).
func (r *tieFileReader) Dimensions() (int, int) {
	w, h, ok := tiethumb.ParseDimensions(r.dims)
	if !ok {
		return 0, 0
	}
	return w, h
}

// dirReader is a folder entry in the preview grid: tapping navigates the
// pane into the directory (gallery.Openable), and the tile shows thumbnails
// of the images inside (gallery.PreviewProvider), badged with the folder
// icon and swipe-cyclable like tie-view's directory tiles.
type dirReader struct {
	entryReader
}

// Open implements gallery.Openable.
func (r *dirReader) Open() { r.fm.navigateTo(r.entry.Path) }

// Previews implements gallery.PreviewProvider. It is called lazily from a
// gallery loader goroutine, so listing the directory (a network call for
// tie) off the UI goroutine is fine.
func (r *dirReader) Previews() ([]gallery.CustomReader, error) {
	entries, err := r.fm.registry.For(r.entry.Path).List(r.entry.Path)
	if err != nil {
		return nil, err
	}
	readers := make([]gallery.CustomReader, 0, len(entries))
	for _, e := range entries {
		if e.IsDir || !isImageName(e.Name) {
			continue
		}
		readers = append(readers, fileReaderFor(r.fm, e))
	}
	if len(readers) == 0 {
		return nil, errors.New("no image previews in " + r.entry.Path)
	}
	return readers, nil
}

// fileReaderFor wraps a non-directory entry, tie entries getting the
// filehost-cached variant.
func fileReaderFor(fm *FileManager, e fs.Entry) gallery.CustomReader {
	if fs.IsTie(e.Path) && e.Hash != "" {
		return &tieFileReader{entryReader: entryReader{entry: e, fm: fm}}
	}
	return &entryReader{entry: e, fm: fm}
}

// entryReaderFor maps a listing entry to a gallery reader: folders, images
// and videos are shown; anything else is left out of the preview grid.
func entryReaderFor(fm *FileManager, e fs.Entry) gallery.CustomReader {
	if e.IsDir {
		return &dirReader{entryReader: entryReader{entry: e, fm: fm}}
	}
	if isImageName(e.Name) || isVideoName(e.Name) {
		return fileReaderFor(fm, e)
	}
	return nil
}

// isImageName reports whether name carries a raster-image extension the
// gallery can decode (the formats registered via imaging: jpeg, png, gif,
// bmp, tiff). Detection is by extension, not content sniffing: tie/mtp URIs
// are not openable paths, and even local sniffing would cost a file read per
// entry on the UI goroutine.
func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

// isVideoName reports whether name carries a video extension (the same list
// the gallery uses).
func isVideoName(name string) bool { return gallery.IsVideoFromPath(name) }

// --- the per-pane controller ---

// previewController owns one pane's preview grid: a gallery.Gallery fed from
// the pane's current listing, swapped into the pane's content slot while on.
type previewController struct {
	fm    *FileManager
	cfg   gallery.Config
	thumb gallery.Thumbnailer

	pane    *fyne.Container // sits in fm.content's center slot while on
	gallery *gallery.Gallery
	on      bool
}

func newPreviewController(fm *FileManager, cfg gallery.Config, thumb gallery.Thumbnailer) *previewController {
	p := &previewController{fm: fm, cfg: cfg, thumb: thumb}
	p.pane = container.NewStack()
	p.pane.Hide()
	return p
}

// build creates the embedded gallery on first use. The window is the pane
// facade, so the gallery's SetContent/SetTitle/SetFullScreen calls stay
// inside the pane. Key bindings that would fight tie-fm (quit the app,
// navigate the local filesystem, fullscreen the window) are disabled; the
// pane's toolbar button toggles preview off instead of [Gallery].Quit.
// CreateView is left to setOn: the pagination bar is created by the first
// LoadGallery (in refresh), so the view must be assembled after it.
func (p *previewController) build() {
	cfg := p.cfg
	cfg.Gallery.Quit = nil
	cfg.Gallery.PathLevelUp = nil
	cfg.Image.Quit = nil
	cfg.Image.ShowGallery = nil
	cfg.Image.FullScreen = nil
	cfg.Image.SaveImage = nil
	cfg.Image.RunCmda = nil
	cfg.Image.CmdA = ""

	g := gallery.NewGallery(fyne.CurrentApp(), newPaneWindow(p.fm.win, p.pane), cfg,
		func(t *gallery.Tile) { p.onTileTapped(t) })
	g.Thumbnailer = p.thumb
	g.Init()
	// A file-manager grid is name-oriented: start with labels on.
	g.ToggleLabels()
	p.gallery = g
}

func (p *previewController) onTileTapped(t *gallery.Tile) {
	p.fm.markActive()
	var entry fs.Entry
	switch r := t.Info.CustomReader.(type) {
	case *tieFileReader:
		entry = r.entry
	case *entryReader:
		entry = r.entry
	}
	// A configured association for the file type wins over the built-in
	// viewers: the app is handed a tie: URL, a stream URL, or a materialized
	// copy, per the association's flags. Directory tiles (OnOpen set) keep
	// navigating via ChangeImage below.
	if t.Info.OnOpen == nil && entry.Name != "" && p.fm.cfg != nil {
		if _, ok := p.fm.cfg.AppFor(entry.Name); ok {
			p.fm.openEntry(entry)
			return
		}
	}
	if t.Info.InputIsVideo {
		if entry.Name != "" {
			go p.openVideo(entry)
		}
		return
	}
	p.gallery.ChangeImage(t.Info)
}

// openVideo plays a video tile in the pane via libmpv (mirroring tie-view):
// streamed from the filehost URL when the backend offers one, else from a
// materialized temp copy (removed when playback closes). Runs off the UI
// goroutine; the gallery hand-off is wrapped in fyne.Do.
func (p *previewController) openVideo(e fs.Entry) {
	var src string
	if s, ok := p.fm.registry.For(e.Path).(fs.Streamer); ok {
		if url, err := s.StreamURL(e); err == nil {
			src = url
		}
	}
	tmpFile := ""
	if src == "" {
		path, err := p.fm.registry.For(e.Path).Materialize(e)
		if err != nil {
			fmt.Println("Error materializing video:", err)
			return
		}
		src = path
		if !fs.IsLocal(e.Path) {
			tmpFile = path // a temp copy, not the user's file
		}
	}
	player, err := mpvplayer.NewMPVPlayer(src)
	if err != nil {
		fmt.Println("Error starting video player:", err)
		if tmpFile != "" {
			os.Remove(tmpFile)
		}
		return
	}
	fyne.Do(func() {
		var onClose func()
		if tmpFile != "" {
			onClose = func() { os.Remove(tmpFile) }
		}
		p.gallery.ShowVideo(player, e.Name, onClose)
	})
}

// toggle flips preview mode; setOn is its idempotent form.
func (p *previewController) toggle() { p.setOn(!p.on) }

func (p *previewController) setOn(on bool) {
	if on == p.on {
		return
	}
	p.on = on
	if on {
		if p.gallery == nil {
			p.build()
		}
		p.refresh()
		// Assemble the grid view (incl. the pagination bar created during the
		// refresh) and show it in the pane's center slot. refresh's
		// ChangeGallery already swapped the content into the pane via the
		// facade; CreateView only rebuilds its contents.
		p.gallery.CreateView()
		p.fm.content.Objects[0] = p.pane
		p.pane.Show()
	} else {
		// Leaving preview mode releases an open image or stops a playing
		// video — a hidden pane must not keep playing audio.
		if p.gallery != nil && (p.gallery.ImageViewActive() || p.gallery.VideoActive()) {
			p.gallery.ShowGrid()
		}
		p.fm.content.Objects[0] = p.fm.table.Instance
		p.pane.Hide()
	}
	p.fm.content.Refresh()
}

// refresh re-feeds the gallery from the pane's current (sorted) listing.
// Called on every reload and re-sort while preview is on.
func (p *previewController) refresh() {
	if !p.on || p.gallery == nil {
		return
	}
	readers := make([]gallery.CustomReader, 0, len(p.fm.entries))
	for _, e := range p.fm.entries {
		if r := entryReaderFor(p.fm, e); r != nil {
			readers = append(readers, r)
		}
	}
	p.gallery.ReadCustom(readers)
	// If the listing changed while an image or video was open (or preview was
	// toggled off in between), drop back to the grid first so the pane never
	// shows a stale single-image view over a new listing, and the open
	// image/player resources are released.
	if p.gallery.ImageViewActive() || p.gallery.VideoActive() {
		p.gallery.ShowGrid()
	}
	// ChangeGallery (not LoadGallery) resets to the first page — the listing
	// was replaced — and swaps the grid into the pane via the facade.
	p.gallery.ChangeGallery()
}

// HandleKey routes a window-level key event to the gallery while preview is
// on. Escape backs out one level — from an open image/video to the grid
// (via the gallery's own ShowGallery handling, if still bound) or, from the
// grid, out of preview mode entirely. Returns true when the key was consumed
// (always, while preview is on).
func (p *previewController) HandleKey(key *fyne.KeyEvent) bool {
	if !p.on || p.gallery == nil {
		return false
	}
	if key.Name == fyne.KeyEscape {
		if p.gallery.ImageViewActive() || p.gallery.VideoActive() {
			p.gallery.ShowGrid()
		} else {
			p.setOn(false)
		}
		return true
	}
	p.gallery.KeyPress(key)
	return true
}
