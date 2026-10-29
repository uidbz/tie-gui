package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/gallery"
)

// errCachedFail marks a directory listing that failed earlier this session;
// see readDir.
var errCachedFail = errors.New("cached earlier failure")

// tieFSNode is a file leaf of the tie filesystem tree: the tie File entry
// plus the path of the directory that listed it (a content hash can appear
// under several directories).
type tieFSNode struct {
	client.File
	parent string
}

// tieFSTree backs a widget.Tree with the tie path-based virtual filesystem
// (the tie:/... hierarchy, read through client.ReadTieDir — the same data
// the FUSE mount exposes). Directories are branches, image files are
// leaves. Tree node IDs are slash paths relative to the tie root ("/",
// "/music/album"); a file leaf's ID is its directory's path joined with its
// content hash, which keeps IDs unique even when filenames collide.
type tieFSTree struct {
	ref    *tieRef
	viewer *gallery.Gallery
	tree   *widget.Tree

	mu       sync.Mutex
	dirs     map[string]*client.Directory // dir path -> cached listing
	branches map[string]bool              // node ID -> is a directory
	files    map[string]tieFSNode         // leaf node ID -> file entry
	readers  map[string]*tieReader        // content hash -> reader (keeps thumbHash warm)
	// loading marks paths whose listing fetch is in flight: a Fyne tree
	// re-asks childUIDs on every layout pass, so without this an uncached
	// directory would kick a new network fetch (and spam a possibly dead
	// server) per pass.
	loading map[string]bool
	// ready is set once the constructor has finished wiring the tree;
	// childUIDs kicks no fetch before that (see the constructor).
	ready bool
	// fetchWG counts in-flight kickFetch workers. Production never needs it;
	// headless tests call fetchWG.Wait() to let fetches (and their
	// fyne.Do tree refreshes — the test driver runs those INLINE on the
	// fetch goroutine) finish before the test ends, instead of bleeding
	// into the next test's widget work.
	fetchWG sync.WaitGroup

	// showHidden controls whether hidden directories (names with a leading
	// ".") appear in the tree. Defaults to false; toggled via the gallery
	// ☰ menu.
	showHidden bool
}

// newTieFSTree returns a tree for navigating the tie virtual filesystem.
// Selecting a directory replaces the gallery with the directory's images;
// selecting a file also opens that image. The tree widget is t.tree.
func newTieFSTree(viewer *gallery.Gallery, ref *tieRef) *tieFSTree {
	t := &tieFSTree{
		ref:    ref,
		viewer: viewer,
		dirs:   make(map[string]*client.Directory),
		// "" must be a branch: the tree walk starts at the root node ""
		// and only descends into ChildUIDs for branches, so without this
		// the whole tree renders empty.
		branches: map[string]bool{"": true, "/": true},
		files:    make(map[string]tieFSNode),
		readers:  make(map[string]*tieReader),
		loading:  make(map[string]bool),
	}
	tree := widget.NewTree(
		t.childUIDs,
		t.isBranch,
		func(branch bool) fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.TreeNodeID, branch bool, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(t.displayName(id))
		},
	)
	tree.OnSelected = t.selected
	t.tree = tree
	// Start with the root expanded: with every branch closed the tab would
	// show only the bare "/" node and look empty. That walk runs before
	// ready is set, so it kicks no fetch; the root listing is then primed
	// explicitly. Kicking during the walk instead would let the fetch's
	// apply race the constructor's own writes to the tree widget (the fyne
	// test driver runs fyne.Do INLINE on the fetch goroutine, which makes
	// the race show up under -race; in production the single UI goroutine
	// hides it but leaves the apply ordering indeterminate).
	tree.OpenBranch("/")
	t.mu.Lock()
	t.ready = true
	t.mu.Unlock()
	t.kickFetch("/")
	return t
}

func (t *tieFSTree) childUIDs(uid widget.TreeNodeID) []widget.TreeNodeID {
	if uid == "" {
		return []widget.TreeNodeID{"/"}
	}
	// The tree calls this during layout, so it must never block: an uncached
	// directory kicks a background fetch and renders empty until the fetch
	// lands and refreshes the tree.
	t.mu.Lock()
	if !t.ready {
		t.mu.Unlock()
		return nil // still under construction; the constructor primes the root
	}
	d, ok := t.dirs[uid]
	inflight := t.loading[uid]
	if !ok && !inflight {
		t.loading[uid] = true
	}
	t.mu.Unlock()
	if !ok {
		if !inflight {
			t.kickFetch(uid)
		}
		return nil // renders once the fetch lands and refreshes the tree
	}
	if d == nil {
		return nil // cached failure (already logged when it happened)
	}
	dir := *d

	t.mu.Lock()
	defer t.mu.Unlock()
	children := make([]widget.TreeNodeID, 0, len(dir.SubDirs)+len(dir.Files))
	// dir.SubDirs comes from a map iteration in ReadTieDir; sort by display
	// name for a stable tree. dir.Files is already sorted by filename.
	subPaths := make([]string, 0, len(dir.SubDirs))
	for _, sub := range dir.SubDirs {
		if len(sub.Paths) == 0 {
			continue
		}
		p := strings.TrimPrefix(sub.Paths[0], client.FileURIScheme)
		// The root dir carries a parent edge to itself (CreateTieRootDir),
		// surfacing as a "/" child; skip self-edges so the tree cannot
		// recurse into the same directory forever.
		if p == uid || baseName(p) == "" {
			continue
		}
		// Hidden directories (leading ".") are skipped unless the user
		// enabled them via the gallery ☰ menu toggle.
		if !t.showHidden && strings.HasPrefix(baseName(p), ".") {
			continue
		}
		subPaths = append(subPaths, p)
	}
	sort.Slice(subPaths, func(i, j int) bool { return baseName(subPaths[i]) < baseName(subPaths[j]) })
	for _, p := range subPaths {
		children = append(children, p)
		t.branches[p] = true
	}
	for _, f := range dir.Files {
		if !isImageFile(f) {
			continue
		}
		id := joinNode(uid, f.Uid)
		children = append(children, id)
		t.files[id] = tieFSNode{File: f, parent: uid}
	}
	return children
}

func (t *tieFSTree) isBranch(uid widget.TreeNodeID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.branches[uid]
}

func (t *tieFSTree) displayName(uid widget.TreeNodeID) string {
	if uid == "/" {
		return "/"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.files[uid]; ok {
		return f.Filename
	}
	return baseName(uid)
}

func (t *tieFSTree) selected(uid widget.TreeNodeID) {
	t.mu.Lock()
	f, isFile := t.files[uid]
	t.mu.Unlock()
	dirPath, selectHash := uid, ""
	if isFile {
		dirPath, selectHash = f.parent, f.Uid
	}
	// The Fyne tree re-focuses itself after OnSelected returns (treeNode.Tapped
	// calls canvas.Focus on the tree). Defer the gallery swap so it runs after
	// that: swapping immediately removes the tree from the canvas for a file
	// selection (logging a "failed to focus" error), and for a directory the
	// tree would hold keyboard focus and swallow the gallery's window-level
	// hotkeys.
	fyne.Do(func() {
		t.showDir(dirPath, selectHash)
		if !isFile {
			t.viewer.ReleaseFocus()
		}
		// In the drawer layout the tree is covering the gallery it just
		// changed, so close it and let the user see the result.
		t.viewer.CloseSidebar()
	})
}

// SetShowHidden controls whether hidden directories (names with a leading ".")
// appear in the tree, refreshing the tree so open branches re-evaluate their
// children.
func (t *tieFSTree) SetShowHidden(show bool) {
	t.showHidden = show
	t.tree.Refresh()
}

// showDir replaces the gallery with the image files of the directory at
// dirPath, and opens the one with selectHash when given.
// showDir replaces the gallery with the image files of the directory at
// dirPath, and opens the one with selectHash when given. The listing read
// and reader construction run on a fetch goroutine; the gallery swap and
// selection happen in one fyne.Do pass, dropped if superseded meanwhile
// (newer query, another directory tap, or a collection switch).
func (t *tieFSTree) showDir(dirPath, selectHash string) {
	var selIdx int
	fetchGalleryPost(t.viewer, func() []gallery.CustomReader {
		tc := t.ref.get()
		dir, err := t.readDirFor(tc, dirPath)
		if err != nil {
			fmt.Println("Error reading tie dir", dirPath, ":", err)
			return nil
		}
		readers, idx := t.readersFor(tc, dir, selectHash)
		selIdx = idx
		return readers
	}, func(readers []gallery.CustomReader) {
		t.selectFromListing(readers, selIdx)
	})
}

// showDirUID is showDir by directory UID instead of path. It is used when
// browsing a tagged directory from tag-query results, where the entry's key
// is the DirUID.
func (t *tieFSTree) showDirUID(uid client.DirUID, selectHash string) {
	var selIdx int
	fetchGalleryPost(t.viewer, func() []gallery.CustomReader {
		tc := t.ref.get()
		dir, err := client.ReadTieDir(tc, uid)
		if err != nil {
			fmt.Println("Error reading tie dir", uid, ":", err)
			return nil
		}
		readers, idx := t.readersFor(tc, dir, selectHash)
		selIdx = idx
		return readers
	}, func(readers []gallery.CustomReader) {
		t.selectFromListing(readers, selIdx)
	})
}

// readersFor turns a directory listing into gallery readers (on the fetch
// goroutine): archives first (browsable blobs), then the image files through
// the shared per-hash reader cache. selIdx is the position of selectHash.
func (t *tieFSTree) readersFor(tc *client.TieClient, dir client.Directory, selectHash string) (readers []gallery.CustomReader, selIdx int) {
	readers = make([]gallery.CustomReader, 0, len(dir.Files)+len(dir.Archives))
	selIdx = -1
	host := tieFileHost(tc)
	for _, a := range dir.Archives {
		hash := a.Hash
		readers = append(readers, &tieArchiveReader{
			hash:     hash,
			filename: a.Filename,
			host:     host,
			tc:       tc,
			open:     func() { browseTieArchive(t.viewer, host, hash) },
		})
	}
	for _, f := range dir.Files {
		if !isImageFile(f) {
			continue
		}
		readers = append(readers, t.readerFor(tc, f.Uid))
		if f.Uid == selectHash {
			selIdx = len(readers) - 1
		}
	}
	return readers, selIdx
}

// selectFromListing opens the selected image of a freshly applied listing
// (UI goroutine, right after ChangeGallery).
func (t *tieFSTree) selectFromListing(readers []gallery.CustomReader, selIdx int) {
	if selIdx < 0 || selIdx >= len(readers) {
		return
	}
	info := gallery.NewImageInfoCustomReader(selIdx, readers[selIdx])
	info.Path = readers[selIdx].Path()
	t.viewer.ChangeImage(info)
}

// kickFetch fetches one directory listing off the UI goroutine (for a
// childUIDs cache miss) and refreshes the tree when it lands.
func (t *tieFSTree) kickFetch(dirPath string) {
	tc := t.ref.get()
	t.fetchWG.Add(1)
	go func() {
		defer t.fetchWG.Done()
		_, err := t.readDirFor(tc, dirPath)
		if err != nil {
			fmt.Println("Error reading tie dir", dirPath, ":", err)
		}
		fyne.Do(func() {
			t.mu.Lock()
			delete(t.loading, dirPath)
			t.mu.Unlock()
			t.tree.Refresh()
		})
	}()
}

// reset drops everything cached about the current connection/collection
// (called on a collection or connection switch) and refreshes the tree:
// listings, child derivations, file leaves and the shared per-hash readers
// — their thumbHash/dimensions mappings belong to the old collection.
func (t *tieFSTree) reset() {
	t.mu.Lock()
	t.dirs = make(map[string]*client.Directory)
	t.branches = map[string]bool{"": true, "/": true}
	t.files = make(map[string]tieFSNode)
	t.readers = make(map[string]*tieReader)
	t.loading = make(map[string]bool)
	t.mu.Unlock()
	t.tree.Refresh()
}

// readDirFor returns the (cached) listing of the directory at dirPath
// through tc. A path not tied to any DirUID yields an empty listing.
// Failures are cached too: otherwise a dead server makes the tree widget
// re-query in a tight loop on every refresh (the tree re-asks childUIDs per
// layout pass), spamming the dead server and starving the UI. The cache is
// per-client: a listing read through a client that a collection switch has
// since replaced is not stored into the new collection's cache (reset()
// clears it eagerly; this guards fetches that raced the switch).
func (t *tieFSTree) readDirFor(tc *client.TieClient, dirPath string) (client.Directory, error) {
	t.mu.Lock()
	d, ok := t.dirs[dirPath]
	t.mu.Unlock()
	if ok {
		if d == nil {
			return client.Directory{}, errCachedFail
		}
		return *d, nil
	}

	uid, err := tc.DirUIDFromPath(dirPath)
	if err != nil {
		t.cacheDir(dirPath, nil, tc)
		return client.Directory{}, err
	}
	var dir client.Directory
	if uid != "" {
		dir, err = client.ReadTieDir(tc, uid)
		if err != nil {
			t.cacheDir(dirPath, nil, tc)
			return client.Directory{}, err
		}
	}
	t.cacheDir(dirPath, &dir, tc)
	return dir, nil
}

// cacheDir stores a listing (nil = failure) unless the client it was read
// through is no longer live.
func (t *tieFSTree) cacheDir(dirPath string, d *client.Directory, tc *client.TieClient) {
	if t.ref.get() != tc {
		return
	}
	t.mu.Lock()
	t.dirs[dirPath] = d
	t.mu.Unlock()
}

// readerFor returns the tieReader for a content hash, creating it on first
// use so the filehost thumbnail mapping (thumbHash) survives re-selections.
// Safe from fetch goroutines; the client snapshot decides the filehost.
func (t *tieFSTree) readerFor(tc *client.TieClient, hash string) *tieReader {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r, ok := t.readers[hash]; ok {
		return r
	}
	r := &tieReader{host: tieFileHost(tc), hash: hash}
	t.readers[hash] = r
	return r
}

// isImageFile reports whether a tie file is a single image the gallery can
// show. MediaType ("image/jpeg") is the reliable check: File.TieType
// collapses to unknown-file when a hash carries several tie-type values
// (StringToTieType joins them into one unmapped string), so the tie-type is
// only a fallback for files without a recorded media type.
func isImageFile(f client.File) bool {
	if isRaw(f.MediaType, f.Filename) {
		return false
	}
	if f.MediaType != "" {
		return strings.HasPrefix(f.MediaType, "image/")
	}
	return f.TieType == client.TieImageFile
}

// rawMediaTypes are the MIME types of camera raw formats that tie reports with
// an "image/" prefix. filetype (used by tie to classify files) detects Canon
// CR2 as "image/x-canon-cr2"; other common raws (NEF, DNG, ARW, ...) are
// TIFF-based and surface as "image/tiff", so those are caught by extension
// instead of media type.
var rawMediaTypes = map[string]bool{
	"image/x-canon-cr2":      true,
	"image/x-canon-crw":      true,
	"image/x-nikon-nef":      true,
	"image/x-nikon-nrw":      true,
	"image/x-adobe-dng":      true,
	"image/x-sony-arw":       true,
	"image/x-sony-sr2":       true,
	"image/x-sony-srf":       true,
	"image/x-fuji-raf":       true,
	"image/x-panasonic-rw2":  true,
	"image/x-panasonic-raw":  true,
	"image/x-olympus-orf":    true,
	"image/x-pentax-pef":     true,
	"image/x-samsung-srw":    true,
	"image/x-sigma-x3f":      true,
	"image/x-hasselblad-3fr": true,
	"image/x-epson-erf":      true,
	"image/x-kodak-dcr":      true,
	"image/x-kodak-kdc":      true,
	"image/x-kodak-k25":      true,
	"image/x-minolta-mrw":    true,
	"image/x-mamiya-mef":     true,
	"image/x-leaf-mos":       true,
	"image/x-leica-rwl":      true,
	"image/x-phaseone-iiq":   true,
	"image/x-dcraw":          true,
	"image/x-raw":            true,
}

// rawExtensions are raw camera filename extensions the gallery cannot decode
// (Go's image package has no decoder for them). TIFF-based raws such as NEF,
// DNG and ARW classify as "image/tiff" and are identified here.
var rawExtensions = map[string]bool{
	".3fr": true, ".ari": true, ".arw": true, ".bay": true, ".braw": true,
	".crw": true, ".cr2": true, ".cr3": true, ".cap": true, ".dcs": true,
	".dcr": true, ".dng": true, ".drf": true, ".eip": true, ".erf": true,
	".fff": true, ".gpr": true, ".iiq": true, ".k25": true, ".kdc": true,
	".mdc": true, ".mef": true, ".mos": true, ".mrw": true, ".nef": true,
	".nrw": true, ".obm": true, ".orf": true, ".pef": true, ".ptx": true,
	".pxn": true, ".r3d": true, ".raf": true, ".raw": true, ".rwl": true,
	".rw2": true, ".rwz": true, ".sr2": true, ".srf": true, ".srw": true,
	".x3f": true,
}

// isRaw reports whether a media type or filename identifies a camera raw
// image, which the gallery cannot decode and must not display.
func isRaw(mediaType, filename string) bool {
	if rawMediaTypes[strings.ToLower(mediaType)] {
		return true
	}
	return rawExtensions[strings.ToLower(filepath.Ext(filename))]
}

// baseName returns the last segment of a slash path ("" for "/").
func baseName(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// joinNode joins a directory node ID and a child segment into a node ID.
func joinNode(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}
