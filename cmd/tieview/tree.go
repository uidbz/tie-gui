package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"git.sr.ht/~uid/tie/client"

	"git.sr.ht/~uid/imgview/gallery"
)

// tieFSNode is a file leaf of the tie filesystem tree: the tie File entry
// plus the path of the directory that listed it (a content hash can appear
// under several directories).
type tieFSNode struct {
	client.File
	parent string
}

// tieFSTree backs a widget.Tree with the tie path-based virtual filesystem
// (the file:/... hierarchy, read through client.ReadTieDir — the same data
// the FUSE mount exposes). Directories are branches, image files are
// leaves. Tree node IDs are slash paths relative to the tie root ("/",
// "/music/album"); a file leaf's ID is its directory's path joined with its
// content hash, which keeps IDs unique even when filenames collide.
type tieFSTree struct {
	tie    *client.TieClient
	viewer *gallery.Gallery
	tree   *widget.Tree

	mu       sync.Mutex
	dirs     map[string]*client.Directory // dir path -> cached listing
	branches map[string]bool              // node ID -> is a directory
	files    map[string]tieFSNode         // leaf node ID -> file entry
	readers  map[string]*tieReader        // content hash -> reader (keeps thumbHash warm)
}

// newTieFSTree returns a tree for navigating the tie virtual filesystem.
// Selecting a directory replaces the gallery with the directory's images;
// selecting a file also opens that image. The tree widget is t.tree.
func newTieFSTree(viewer *gallery.Gallery, tc *client.TieClient) *tieFSTree {
	t := &tieFSTree{
		tie:    tc,
		viewer: viewer,
		dirs:   make(map[string]*client.Directory),
		// "" must be a branch: the tree walk starts at the root node ""
		// and only descends into ChildUIDs for branches, so without this
		// the whole tree renders empty.
		branches: map[string]bool{"": true, "/": true},
		files:    make(map[string]tieFSNode),
		readers:  make(map[string]*tieReader),
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
	// Start with the root expanded: with every branch closed the tab would
	// show only the bare "/" node and look empty.
	tree.OpenBranch("/")
	t.tree = tree
	return t
}

func (t *tieFSTree) childUIDs(uid widget.TreeNodeID) []widget.TreeNodeID {
	if uid == "" {
		return []widget.TreeNodeID{"/"}
	}
	dir, err := t.readDir(uid)
	if err != nil {
		fmt.Println("Error reading tie dir", uid, ":", err)
		return nil
	}

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
	t.showDir(dirPath, selectHash)
}

// showDir replaces the gallery with the image files of the directory at
// dirPath, and opens the one with selectHash when given.
func (t *tieFSTree) showDir(dirPath, selectHash string) {
	dir, err := t.readDir(dirPath)
	if err != nil {
		fmt.Println("Error reading tie dir", dirPath, ":", err)
		return
	}
	t.showListing(dir, selectHash)
}

// showDirUID is showDir by directory UID instead of path. It is used when
// browsing a tagged directory from tag-query results, where the entry's key
// is the DirUID.
func (t *tieFSTree) showDirUID(uid client.DirUID, selectHash string) {
	dir, err := client.ReadTieDir(t.tie, uid)
	if err != nil {
		fmt.Println("Error reading tie dir", uid, ":", err)
		return
	}
	t.showListing(dir, selectHash)
}

// showListing replaces the gallery with the image files of a directory
// listing, and opens the one with selectHash when given.
func (t *tieFSTree) showListing(dir client.Directory, selectHash string) {
	readers := make([]gallery.CustomReader, 0, len(dir.Files)+len(dir.Archives))
	selIdx := -1
	host := tieFileHost(t.tie)
	for _, a := range dir.Archives {
		hash := a.Hash
		readers = append(readers, &tieArchiveReader{
			hash:     hash,
			filename: a.Filename,
			host:     host,
			open:     func() { browseTieArchive(t.viewer, host, hash) },
		})
	}
	for _, f := range dir.Files {
		if !isImageFile(f) {
			continue
		}
		readers = append(readers, t.reader(f.Uid))
		if f.Uid == selectHash {
			selIdx = len(readers) - 1
		}
	}
	t.viewer.ReadCustomAsync(func() []gallery.CustomReader { return readers })
	t.viewer.ChangeGallery()
	if selIdx >= 0 {
		info := gallery.NewImageInfoCustomReader(selIdx, readers[selIdx])
		info.Path = readers[selIdx].Path()
		t.viewer.ChangeImage(info)
	}
}

// readDir returns the (cached) listing of the directory at dirPath. A path
// not tied to any DirUID yields an empty listing.
func (t *tieFSTree) readDir(dirPath string) (client.Directory, error) {
	t.mu.Lock()
	d, ok := t.dirs[dirPath]
	t.mu.Unlock()
	if ok {
		return *d, nil
	}

	uid, err := t.tie.DirUIDFromPath(dirPath)
	if err != nil {
		return client.Directory{}, err
	}
	var dir client.Directory
	if uid != "" {
		dir, err = client.ReadTieDir(t.tie, uid)
		if err != nil {
			return client.Directory{}, err
		}
	}
	t.mu.Lock()
	t.dirs[dirPath] = &dir
	t.mu.Unlock()
	return dir, nil
}

// reader returns the tieReader for a content hash, creating it on first use
// so the filehost thumbnail mapping (thumbHash) survives re-selections.
func (t *tieFSTree) reader(hash string) *tieReader {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r, ok := t.readers[hash]; ok {
		return r
	}
	r := &tieReader{host: tieFileHost(t.tie), hash: hash}
	t.readers[hash] = r
	return r
}

// isImageFile reports whether a tie file is a single image the gallery can
// show. MediaType ("image/jpeg") is the reliable check: File.TieType
// collapses to unknown-file when a hash carries several tie-type values
// (StringToTieType joins them into one unmapped string), so the tie-type is
// only a fallback for files without a recorded media type.
func isImageFile(f client.File) bool {
	if f.MediaType != "" {
		return strings.HasPrefix(f.MediaType, "image/")
	}
	return f.TieType == client.TieImageFile
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
