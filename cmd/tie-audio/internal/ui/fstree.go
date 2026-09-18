package ui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
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
// the FUSE mount exposes). Directories are branches, audio files and
// audio-archives are leaves. Tree node IDs are slash paths relative to the
// tie root ("/", "/music/album"); a file leaf's ID is its directory's path
// joined with its content hash, which keeps IDs unique even when filenames
// collide.
type tieFSTree struct {
	page *browsePage // session access + album opener
	tree *widget.Tree

	mu       sync.Mutex
	dirs     map[string]*client.Directory // dir path -> cached listing
	branches map[string]bool              // node ID -> is a directory
	files    map[string]tieFSNode         // leaf node ID -> file entry

	// currentDir is the directory whose listing the cover wall currently
	// shows ("" when the wall is fed by a tag query), so reload can re-read
	// and re-show it.
	currentDir string

	// showHidden controls whether hidden directories (names with a leading
	// ".") appear in the tree. Defaults to false; toggled via the gallery
	// ☰ menu.
	showHidden bool
}

// newTieFSTree returns a tree for navigating the tie virtual filesystem.
// Selecting a directory replaces the cover wall with the directory's albums
// and tracks; selecting a file opens it as a single-track album. The tree
// widget is t.tree.
func newTieFSTree(page *browsePage) *tieFSTree {
	t := &tieFSTree{
		page: page,
		dirs: make(map[string]*client.Directory),
		// "" must be a branch: the tree walk starts at the root node ""
		// and only descends into ChildUIDs for branches, so without this
		// the whole tree renders empty.
		branches: map[string]bool{"": true, "/": true},
		files:    make(map[string]tieFSNode),
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
		if p == uid || fsBaseName(p) == "" {
			continue
		}
		// Hidden directories (leading ".") are skipped unless the user
		// enabled them via the gallery ☰ menu toggle.
		if !t.showHidden && strings.HasPrefix(fsBaseName(p), ".") {
			continue
		}
		subPaths = append(subPaths, p)
	}
	sort.Slice(subPaths, func(i, j int) bool { return fsBaseName(subPaths[i]) < fsBaseName(subPaths[j]) })
	for _, p := range subPaths {
		children = append(children, p)
		t.branches[p] = true
	}
	for _, f := range dir.Files {
		if !isAudioFile(f) {
			continue
		}
		id := joinNode(uid, f.Uid)
		children = append(children, id)
		t.files[id] = tieFSNode{File: f, parent: uid}
	}
	// Audio-archives surface as leaves that open as albums, like the tag
	// wall's archive tiles.
	for _, a := range dir.Archives {
		if a.TieType != client.TieAudioArchive {
			continue
		}
		id := joinNode(uid, a.Hash)
		children = append(children, id)
		t.files[id] = tieFSNode{File: client.File{Uid: a.Hash, Filename: a.Filename, TieType: a.TieType}, parent: uid}
	}
	return children
}

func (t *tieFSTree) isBranch(uid widget.TreeNodeID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.branches[uid]
}

// isAudioFile reports whether a tie file is playable audio, mirroring the
// data layer's classification: MediaType ("audio/flac") is the reliable
// check, File.TieType the fallback for files without a recorded media type
// (a hash carrying several tie-type values collapses to unknown-file).
func isAudioFile(f client.File) bool {
	return f.TieType == client.TieAudioFile || strings.HasPrefix(f.MediaType, "audio/")
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
	return fsBaseName(uid)
}

func (t *tieFSTree) selected(uid widget.TreeNodeID) {
	t.mu.Lock()
	f, isFile := t.files[uid]
	t.mu.Unlock()
	if isFile {
		t.openTrack(f)
		t.page.closeSidebar()
		return
	}
	// The Fyne tree re-focuses itself after OnSelected returns (treeNode.Tapped
	// calls canvas.Focus on the tree). Defer the gallery swap so it runs after
	// that, or the tree would hold keyboard focus and swallow the gallery's
	// window-level hotkeys.
	fyne.Do(func() {
		t.showDir(uid)
		t.page.viewer.ReleaseFocus()
		// In the compact layout the drawer is covering the wall it just
		// changed; close it so the user sees the albums they picked.
		t.page.closeSidebar()
	})
}

// SetShowHidden controls whether hidden directories (names with a leading ".")
// appear in the tree, refreshing the tree so open branches re-evaluate their
// children.
func (t *tieFSTree) SetShowHidden(show bool) {
	t.showHidden = show
	t.tree.Refresh()
}

// openTrack opens an audio file leaf as a single-track album, or an
// audio-archive leaf as its track listing.
func (t *tieFSTree) openTrack(f tieFSNode) {
	if f.TieType == client.TieAudioArchive {
		t.page.openAlbum(data.Album{UID: f.Uid, Kind: data.AlbumArchive, Title: f.Filename})
		return
	}
	t.page.openAlbum(data.Album{UID: f.Uid, Kind: data.AlbumTrack, Title: f.Filename})
}

// showDir replaces the cover wall with the albums and tracks of the
// directory at dirPath.
func (t *tieFSTree) showDir(dirPath string) {
	dir, err := t.readDir(dirPath)
	if err != nil {
		fmt.Println("Error reading tie dir", dirPath, ":", err)
		return
	}
	t.mu.Lock()
	t.currentDir = dirPath
	t.mu.Unlock()
	t.page.feed = feedDir
	t.showListing(dir)
}

// reset drops every cached listing and title, re-seeds the tree's branch set
// and refreshes the tree, so the next expansion re-queries the server. Used
// on a collection switch (the cache would otherwise keep showing the previous
// collection's tree for the rest of the run) and by reload.
func (t *tieFSTree) reset() {
	t.mu.Lock()
	t.dirs = make(map[string]*client.Directory)
	t.branches = map[string]bool{"": true, "/": true}
	t.files = make(map[string]tieFSNode)
	t.currentDir = ""
	t.mu.Unlock()
	t.tree.Refresh()
}

// reload re-reads the tie tree from the server: cached listings are dropped
// and the directory currently shown on the cover wall (if any) is re-read and
// re-shown, so content imported since the listings were cached appears
// without restarting the app.
func (t *tieFSTree) reload() {
	t.mu.Lock()
	current := t.currentDir
	t.mu.Unlock()
	t.reset()
	if current != "" {
		t.showDir(current) // re-reads (cache dropped) and re-sets currentDir
	}
}

// showListing replaces the cover wall with the albums of a directory
// listing: each subdirectory becomes an album tile, followed by the
// directory's standalone audio files as single-track albums and its
// audio-archives as archive albums.
func (t *tieFSTree) showListing(dir client.Directory) {
	albums := make([]data.Album, 0, len(dir.SubDirs)+len(dir.Files))
	// SubDirs arrives from a map iteration; sort by name for a stable wall,
	// matching the tree's child order. The root's self-edge is skipped.
	subPaths := make([]string, 0, len(dir.SubDirs))
	byPath := make(map[string]client.SubDirectory, len(dir.SubDirs))
	for _, sub := range dir.SubDirs {
		if len(sub.Paths) == 0 {
			continue
		}
		p := strings.TrimPrefix(sub.Paths[0], client.FileURIScheme)
		if fsBaseName(p) == "" {
			continue
		}
		subPaths = append(subPaths, p)
		byPath[p] = sub
	}
	sort.Strings(subPaths)
	for _, p := range subPaths {
		sub := byPath[p]
		// Prefer the subdirectory's own album title when it carries one
		// (e.g. the playlists tie-audio saves name themselves); the folder
		// name (often "Artist - Album") is the fallback.
		albums = append(albums, data.Album{
			UID:   string(sub.Uid),
			Kind:  data.AlbumDir,
			Title: t.subTitle(sub.Uid, fsBaseName(p)),
		})
	}
	for _, f := range dir.Files {
		if !isAudioFile(f) {
			continue
		}
		albums = append(albums, data.Album{UID: f.Uid, Kind: data.AlbumTrack, Title: f.Filename})
	}
	for _, a := range dir.Archives {
		if a.TieType != client.TieAudioArchive {
			continue
		}
		albums = append(albums, data.Album{UID: a.Hash, Kind: data.AlbumArchive, Title: a.Filename})
	}
	t.page.setWallAlbums(albums)
	t.page.viewer.ReadCustomAsync(func() []gallery.CustomReader { return t.page.readers(albums) })
	t.page.showWall()
}

// subTitle resolves a subdirectory's display title: its own "album" or
// "name" triple when recorded (saved playlists carry one), else the fallback
// (the folder name). The lookup is cached like the directory listings so a
// re-shown wall doesn't re-query per entry.
func (t *tieFSTree) subTitle(uid client.DirUID, fallback string) string {
	t.mu.Lock()
	key := "title:" + string(uid)
	cached, ok := t.dirs[key]
	t.mu.Unlock()
	if ok {
		if cached == nil {
			return fallback
		}
		return cached.Paths[0]
	}
	title := ""
	if row, err := t.page.session.Tie.Get(string(uid)); err == nil {
		title = client.RowFirst(row, client.TieAlbum.String())
		if title == "" {
			title = client.RowFirst(row, client.TieName.String())
		}
	}
	t.mu.Lock()
	if title == "" {
		t.dirs[key] = nil
	} else {
		t.dirs[key] = &client.Directory{Paths: []string{title}}
	}
	t.mu.Unlock()
	if title == "" {
		return fallback
	}
	return title
}

// readDir returns the (cached) listing of the directory at dirPath. A path
// not tied to any DirUID yields an empty listing — uncached, so a directory
// imported at that path later this session appears on the next read. Failures
// are cached: otherwise a dead server makes the tree widget re-query in a
// tight loop on every refresh (the tree re-asks childUIDs per layout pass),
// spamming the dead server and starving the UI. Successful listings are
// cached for the session; a wall reload (reloadWall) and a collection switch
// drop the cache.
func (t *tieFSTree) readDir(dirPath string) (client.Directory, error) {
	t.mu.Lock()
	d, ok := t.dirs[dirPath]
	t.mu.Unlock()
	if ok {
		if d == nil {
			return client.Directory{}, errCachedFail
		}
		return *d, nil
	}

	tc := t.page.session.Tie
	uid, err := tc.DirUIDFromPath(dirPath)
	if err != nil {
		t.mu.Lock()
		t.dirs[dirPath] = nil
		t.mu.Unlock()
		return client.Directory{}, err
	}
	if uid == "" {
		// Not tied to any DirUID (yet): an empty listing, deliberately not
		// cached — caching it would hide a directory imported here after the
		// first read for the rest of the session.
		return client.Directory{}, nil
	}
	dir, err := client.ReadTieDir(tc, uid)
	if err != nil {
		t.mu.Lock()
		t.dirs[dirPath] = nil
		t.mu.Unlock()
		return client.Directory{}, err
	}
	t.mu.Lock()
	t.dirs[dirPath] = &dir
	t.mu.Unlock()
	return dir, nil
}

// fsBaseName returns the last segment of a slash path ("" for "/").
func fsBaseName(p string) string {
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
