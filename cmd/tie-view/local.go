package main

// Local filesystem support. tie-view's first positional argument may be a
// local path (directory, image, or archive) instead of a tie: URL; such an
// argument is opened exactly the way imgview opens it: a directory becomes
// the gallery (subdirectories and archives browsable, videos playable), an
// image opens full-size inside its parent directory's gallery, and an
// archive opens on its image members. Navigation (tile taps, PathLevelUp)
// is the gallery's own local-directory machinery, so the whole flow works
// without a tie server.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"github.com/uidbz/tie/metadata"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/mpvplayer"
)

// localInput is how a positional argument classifies as a local path.
type localInput int

const (
	// localNone: the argument is not a local path — it is empty, a tie:
	// URL, a bare content hash, or simply does not exist on the local
	// filesystem (the tie virtual-filesystem resolution then decides).
	localNone localInput = iota
	localDir
	localImage
	localArchive
	// localUnsupported: the path exists but is neither a directory nor a
	// supported image/archive (imgview's "Unknown input type").
	localUnsupported
)

// classifyLocalInput decides whether the positional argument is a local
// filesystem path tie-view should open like imgview. tie: URLs and bare
// content hashes stay tie subjects; a path that does not exist locally is
// left for the tie virtual-filesystem resolution (loadTieURL).
func classifyLocalInput(arg string) (string, localInput) {
	if arg == "" || strings.HasPrefix(arg, "tie:") || metadata.IsHexHash(arg) {
		return "", localNone
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", localNone
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", localNone
	}
	switch {
	case fi.IsDir():
		return abs, localDir
	case gallery.IsArchiveFromPath(abs):
		return abs, localArchive
	case gallery.IsImageFromPath(abs):
		return abs, localImage
	}
	return abs, localUnsupported
}

// openLocalVideo plays a local video entry with libmpv. Local entries carry
// a real filesystem path, so they play in place — no temp copy, unlike
// reader-backed tie entries (openTieVideo).
func openLocalVideo(viewer *gallery.Gallery, info *gallery.ImageInfo) {
	player, err := mpvplayer.NewMPVPlayer(info.Path)
	if err != nil {
		fmt.Println("Error starting video player:", err)
		return
	}
	fyne.Do(func() { viewer.ShowVideo(player, info.DisplayName, nil) })
}
