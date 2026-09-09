package main

// tie: URL support. tie-view accepts a tie: URL as its first positional
// argument and loads what it points at:
//
//	tie:<hash>  (also tie://<hash>, or a bare 64-hex hash)
//	            a single subject by content hash or DirUID — an image is
//	            displayed, a video played, a directory browsed, an archive
//	            opened on its image members.
//	tie:/virtual/path
//	            a path in the tie virtual filesystem — a directory is browsed;
//	            a file (or archive) leaf resolves to its content hash and is
//	            handled like the hash form above.
//
// tie-fm hands the hash form to applications whose file association carries
// the "App understands tie: URLs" flag, so such an app can be set as the
// opener for image/video types there.

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/uidbz/tie/client"
	"github.com/uidbz/tie/metadata"

	"github.com/uidbz/tie-gui/gallery"
)

// loadTieURL loads the subject of a tie: URL into the viewer, replacing the
// default startup view. Call it on its own goroutine: tie client calls run
// synchronously here, and all viewer mutations go through fyne.Do.
func loadTieURL(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID), rawURL string) {
	rest := strings.TrimPrefix(rawURL, "tie://")
	rest = strings.TrimPrefix(rest, "tie:")
	rest = strings.TrimSpace(rest)
	switch {
	case rest == "":
		tieURLError(window, rawURL, errors.New("empty tie URL"))
	case metadata.IsHexHash(rest):
		loadTieHash(window, viewer, tc, fsTree, browseDir, rawURL, rest)
	default:
		loadTiePath(window, viewer, tc, fsTree, browseDir, rawURL, rest)
	}
}

// loadTieHash loads a single tie subject by key. Content hashes and DirUIDs
// are both 64-char hex, so the subject's triples decide what it is.
func loadTieHash(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID), rawURL, hash string) {
	row, err := tc.Get(hash)
	if errors.Is(err, client.ErrNotFound) {
		// No metadata triples (a blob that was never imported): still try to
		// display it as a plain image — the filehost may serve it.
		showTieSubject(viewer, []gallery.CustomReader{&tieReader{host: tieFileHost(tc), hash: hash}})
		return
	}
	if err != nil {
		tieURLError(window, rawURL, err)
		return
	}
	types := client.RowValues(row, client.TieTypeProperty.String())
	if slices.Contains(types, client.TieImageDir.String()) || slices.Contains(types, client.TieDirectory.String()) {
		loadTieDirUID(window, viewer, tc, fsTree, rawURL, client.DirUID(hash))
		return
	}
	switch classifyTieRow(row) {
	case tieRowFile, tieRowVideo:
		showTieSubject(viewer, buildReaders(viewer, tc, []client.Row{row}, browseDir))
	case tieRowArchive:
		// browseTieArchive fetches the blob inside ReadCustomAsync and
		// blocks in ChangeGallery until it lands — the same path archive
		// tiles take when tapped.
		fyne.Do(func() { browseTieArchive(viewer, tieFileHost(tc), hash) })
	default:
		tieURLError(window, rawURL, fmt.Errorf("not an image, video, directory or archive (media type %q)",
			client.RowFirst(row, client.TieMediaType.String())))
	}
}

// loadTieDirUID browses a tie directory: the listing is fetched here (on the
// caller's goroutine) and swapped into the gallery on the UI goroutine.
func loadTieDirUID(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, rawURL string, uid client.DirUID) {
	dir, err := client.ReadTieDir(tc, uid)
	if err != nil {
		tieURLError(window, rawURL, err)
		return
	}
	fyne.Do(func() { fsTree.showListing(dir, "") })
}

// loadTiePath resolves a virtual-filesystem path and loads it: a directory
// is browsed; a file or archive leaf is handled by its content hash like the
// bare-hash form.
func loadTiePath(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID), rawURL, path string) {
	path = "/" + strings.Trim(path, "/")
	st, err := tc.StatPath(path, client.StatOptions{})
	if err != nil {
		tieURLError(window, rawURL, err)
		return
	}
	if st.Kind == client.StatDirectory {
		loadTieDirUID(window, viewer, tc, fsTree, rawURL, client.DirUID(st.Key))
		return
	}
	loadTieHash(window, viewer, tc, fsTree, browseDir, rawURL, st.Key)
}

// showTieSubject replaces the gallery with a single subject and opens it: an
// image is displayed full-size, a video starts playing. The one-item gallery
// stays behind so Q/Escape/Back has a grid to return to.
func showTieSubject(viewer *gallery.Gallery, readers []gallery.CustomReader) {
	if len(readers) == 0 {
		return
	}
	viewer.ReadCustomAsync(func() []gallery.CustomReader { return readers })
	fyne.Do(func() {
		// ChangeGallery blocks on the (trivial) fetch above; it does no UI
		// work, so waiting on the UI goroutine is safe.
		viewer.ChangeGallery()
		info := gallery.NewImageInfoCustomReader(0, readers[0])
		info.Path = readers[0].Path()
		if vf, ok := readers[0].(gallery.VideoFile); ok && vf.IsVideo() {
			go openTieVideo(viewer, info)
			return
		}
		viewer.ChangeImage(info)
	})
}

// tieURLError reports a tie: URL that could not be loaded, on stderr and in
// a dialog.
func tieURLError(window fyne.Window, rawURL string, err error) {
	fmt.Println("Error loading", rawURL+":", err)
	fyne.Do(func() { dialog.ShowError(fmt.Errorf("cannot load %s: %w", rawURL, err), window) })
}
