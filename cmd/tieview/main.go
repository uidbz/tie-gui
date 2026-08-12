package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"

	"git.sr.ht/~uid/conf"
	"git.sr.ht/~uid/tie/client"

	"git.sr.ht/~uid/imgview/gallery"
	"git.sr.ht/~uid/imgview/mpvplayer"
	"git.sr.ht/~uid/imgview/tagselection"
	// "github.com/pkg/profile"
)

//go:embed Icon.png
var icon []byte

func main() {
	// defer profile.Start(profile.CPUProfile).Stop()
	// defer profile.Start().Stop()
	tieTag := flag.String("tag", "favorite", "Show images with tag")
	tieConfigName := gallery.ConfigFlag("Tie config file to load: a name searched in tie's config dirs (like `tie -c`), or a file path (default: config.toml from the user config dir)")
	flag.StringVar(&tieHostName, "host", "", "Fetch content from this filehost named in the tie config (default: \"fast\" when configured, else the first DefaultFileHosts entry)")
	flag.Parse()

	myApp, myWindow := gallery.NewApp("sr.ht.uid.tieview", "tieview", icon)

	config := gallery.LoadConfig(myWindow, "")

	tieConfig := loadTieConfig(gallery.NormalizeConfigPath(*tieConfigName))
	applyTiePrefs(myApp, &tieConfig)
	if tieHostName != "" {
		if _, ok := tieConfig.FileHosts[tieHostName]; !ok {
			fmt.Fprintf(os.Stderr, "No filehost %q in the tie config. Configured filehosts: %s\n", tieHostName, strings.Join(fileHostNames(tieConfig), ", "))
			os.Exit(2)
		}
	}
	tieClient := client.NewTieClient(tieConfig)

	tagger := newImageTagger(myWindow, tieClient)
	// taggerOverlay is a persistent border container that anchors the tag
	// panel to the bottom of the image view. It is added to viewer.Content
	// whenever a single image is displayed, so the panel can be shown/hidden
	// by toggling tagger.Panel without rebuilding the content stack.
	taggerOverlay := container.NewBorder(nil, tagger.Panel, nil, nil)

	viewer := gallery.NewGallery(myApp, myWindow, config, func(t *gallery.Tile) {
		if t.Info.InputIsVideo {
			go openTieVideo(myApp, t.Info)
			return
		}
		t.Viewer.ChangeImage(t.Info)
	})
	viewer.Thumbnailer = &filehostThumbnailer{tie: tieClient, tileWidth: int(config.General.TileWidth)}
	toggleTagger := func() {
		tagger.Toggle(tagger.hash)
		viewer.Content.Refresh()
	}
	if fyne.CurrentDevice().IsMobile() {
		// On mobile a tap is used for other interactions (focus, navigation).
		// Use a swipe-up gesture instead to open/close the tag panel.
		viewer.OnSwipeUp = toggleTagger
	} else {
		// On desktop a single tap on the image opens/closes the tag panel.
		viewer.OnTapped = toggleTagger
	}
	// Restore keyboard focus to the image view when the panel is closed so
	// that hotkeys (next/prev, zoom, etc.) keep working on desktop.
	tagger.OnHide = func() {
		if !fyne.CurrentDevice().IsMobile() {
			myWindow.Canvas().Focus(viewer.CurrentImageView)
		}
	}
	fsTree := newTieFSTree(viewer, tieClient)
	browseDir := func(uid client.DirUID) { fsTree.showDirUID(uid, "") }
	viewer.Sidebar = makeSidebar(myApp, myWindow, viewer, tieClient, fsTree, browseDir, tagger)

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	// Right-click on a gallery tile shows a de-import confirmation dialog.
	viewer.OnTileSecondaryTapped = func(t *gallery.Tile) {
		if t.Info.CustomReader == nil {
			return
		}
		var kind, label string
		switch r := t.Info.CustomReader.(type) {
		case *tieReader:
			kind = "file"
			label = r.hash[:16] + "…"
		case *tieDirReader:
			kind = "directory"
			label = string(r.uid)[:16] + "…"
		default:
			return
		}
		msg := fmt.Sprintf("Remove all metadata and tags exclusively used by this %s?\n\n%s\n\nThis cannot be undone.", kind, label)
		dialog.ShowConfirm("De-import "+kind, msg, func(confirmed bool) {
			if !confirmed {
				return
			}
			go func() {
				var err error
				switch r := t.Info.CustomReader.(type) {
				case *tieReader:
					err = deimportFile(tieClient, r.hash)
				case *tieDirReader:
					err = deimportDir(tieClient, r.uid)
				}
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("de-import failed: %w", err), myWindow)
						return
					}
					viewer.RemoveItem(t.Info)
					viewer.ChangeGallery()
				})
			}()
		}, myWindow)
	}

	readFromTie(viewer, tieClient, []string{*tieTag}, nil, "tag", browseDir)
	// readFromTie(viewer, tieClient, []string{"4"}, nil, "rating", browseDir)

	viewer.OnImageChange = func(info *gallery.ImageInfo) {
		// Focusing the image view drives keyboard navigation on desktop, but on
		// mobile it summons the soft keyboard (the view is Focusable). Skip it.
		if !fyne.CurrentDevice().IsMobile() {
			myWindow.Canvas().Focus(viewer.CurrentImageView)
		}
		// Overlay the tag panel on top of the image view. ChangeImage already
		// set Content.Objects to [CurrentImage]; append the taggerOverlay so
		// the panel can be toggled without rebuilding the content stack.
		viewer.Content.Objects = append(viewer.Content.Objects, taggerOverlay)
		viewer.Content.Refresh()
		// Track which image is displayed so Toggle opens the correct panel.
		if tr, ok := info.CustomReader.(*tieReader); ok {
			tagger.SetCurrentHash(tr.hash)
		} else {
			tagger.SetCurrentHash("")
		}
	}

	myWindow.SetContent(viewer.Content)
	viewer.LoadGallery()
	viewer.CreateView()

	myWindow.Resize(fyne.NewSize(config.General.DefaultWidth, config.General.DefaultHeight))

	myWindow.ShowAndRun()
}

// loadTieConfig loads the tie client config. Without a -config argument it
// reads config.toml from the user config dir only: conf.LoadConfig also
// searches the current directory, where any stray config.toml (e.g.
// imgview's own) would shadow the tie config. A -config value containing a
// path separator is read as an explicit file path; any other value is a
// config name searched in tie's config dirs, mirroring `tie -c`.
func loadTieConfig(name string) client.Config {
	tieConfig := client.Config{}
	var err error
	switch {
	case name == "":
		_, err = conf.LoadFromUserConfigDir("tie", "config.toml", &tieConfig)
	case strings.ContainsRune(name, '/'):
		err = conf.ReadConfig(name, &tieConfig)
	default:
		_, err = conf.LoadConfig("tie", name, &tieConfig)
	}
	if err != nil {
		fmt.Println("Error reading tie config:", err)
		return client.DefaultConfig()
	}
	return tieConfig
}

// fileHostNames returns the sorted names of the filehosts in a tie config,
// for error messages.
func fileHostNames(c client.Config) []string {
	names := make([]string, 0, len(c.FileHosts))
	for name := range c.FileHosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// makeSidebar builds the navigation sidebar: the first tab browses images
// by tag, the second navigates the tie virtual filesystem.
func makeSidebar(a fyne.App, window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID), tagger *imageTagger) *container.AppTabs {
	tagWidget, reloadTags := makeTagSidebar(window, viewer, tc, browseDir, tagger)
	return container.NewAppTabs(
		container.NewTabItem("Tags", tagWidget),
		container.NewTabItem("Files", fsTree.tree),
		makeSettingsTab(a, tc, reloadTags),
	)
}

// makeTagSidebar builds the tag sidebar: it lists the tags registered under
// the "tags" key and re-queries the gallery whenever the selection changes.
// When tags are selected the list is narrowed to tags that co-occur with the
// current selection (faceted refinement via CoTagsForQueryExcludingInput).
// Clearing the selection restores the original full list.
//
// It returns the widget and a reloadTags function. reloadTags clears the
// current selection and tag lists and re-fetches tags from the (possibly
// reconfigured) live client — call it after switching profiles.
//
// tagger, when non-nil, receives the full tag list via SetAllTags whenever
// the tag list is (re-)loaded. This keeps the image-tagger search trie in
// sync with the sidebar's tag list without a separate network request.
func makeTagSidebar(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, browseDir func(client.DirUID), tagger *imageTagger) (*tagselection.TagSelection, func()) {
	ts := tagselection.NewTagSelection(window)
	// Sidebar uses include/exclude filtering, so show the checkbox.
	ts.ShowIncludeExclude = true

	// allTags and allFavorites hold the full unfiltered lists from the most
	// recent tag fetch. All reads and writes happen on the UI goroutine
	// (inside fyne.Do), so no mutex is needed.
	var allTags []string
	var allFavorites []string
	var allFavoritesLabel string

	// reloadTags clears the widget state and re-fetches tags from tc.
	// Safe to call at any time; the network fetch runs in a goroutine.
	reloadTags := func() {
		fyne.Do(func() {
			ts.ClearSelected()
			ts.ClearAllTags()
			ts.ClearFavorites()
			allTags = nil
			allFavorites = nil
			allFavoritesLabel = ""
			ts.SetListLabel("Loading…")
		})
		go func() {
			row, err := tc.Get("tags")
			if err != nil {
				fmt.Println("Error getting tags:", err)
				fyne.Do(func() { ts.SetListLabel("Error loading tags") })
				return
			}
			fyne.Do(func() {
				allTags = client.RowValues(row, "all")
				for _, tag := range allTags {
					ts.AddTag(tag)
				}
			// Capture the actual tie favorites before the sidebar fallback
			// so the tagger's ☆/★ state reflects the real relation, not the
			// "show everything" substitute used when no favorites are configured.
			actualFavorites := client.RowValues(row, "favorite")
			allFavorites = actualFavorites
			if len(allFavorites) == 0 {
				// No favorites configured: list every tag so the sidebar
				// isn't empty until something is typed in the search box.
				allFavorites = allTags
				allFavoritesLabel = "All tags"
			} else {
				allFavoritesLabel = "Favorites"
			}
			ts.SetListLabel(allFavoritesLabel)
			ts.SetFavorites(allFavorites)
			if tagger != nil {
				tagger.SetAllTags(allTags)
				tagger.SetFavoriteTags(actualFavorites)
			}
		})
	}()
}

reloadTags() // initial load

ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		readFromTie(viewer, tc, in, ex, "tag", browseDir)
		viewer.ChangeGallery()

		// Refresh the tag list in the background. Copies of in/ex are
		// passed so the goroutine is safe from later UI changes.
		inSnap := append([]string(nil), in...)
		exSnap := append([]string(nil), ex...)
		go func() {
			if len(inSnap) == 0 && len(exSnap) == 0 {
				// Nothing selected: restore the original full list.
				fyne.Do(func() {
					ts.ClearAllTags()
					for _, tag := range allTags {
						ts.AddTag(tag)
					}
					ts.SetListLabel(allFavoritesLabel)
					ts.SetFavorites(allFavorites)
				})
				return
			}
			coTags, err := tc.CoTagsForQueryExcludingInput(inSnap, exSnap, "")
			if err != nil {
				fmt.Println("Error getting co-tags:", err)
				return
			}
			fyne.Do(func() {
				ts.ClearAllTags()
				for _, tag := range coTags {
					ts.AddTag(tag)
				}
				if len(coTags) > 0 {
					ts.SetListLabel("Related tags")
				} else {
					ts.SetListLabel("No related tags")
				}
				ts.SetFavorites(coTags)
			})
		}()
	}

	return ts, reloadTags
}

// openTieVideo opens a libmpv video player window for a tie video entry.
// It streams directly from the filehost URL when available; otherwise it
// falls through to downloading the content to a temporary file.
func openTieVideo(a fyne.App, info *gallery.ImageInfo) {
	var src string
	var tmpFile string

	if vs, ok := info.CustomReader.(gallery.VideoStreamer); ok {
		src = vs.StreamURL()
	}
	if src == "" {
		// Fallback: download the blob to a temp file.
		r, err := info.CustomReader.GetReader()
		if err != nil {
			fmt.Println("Error reading video:", err)
			return
		}
		tmp, err := os.CreateTemp("", "imgview-tie-vid-*")
		if err != nil {
			return
		}
		if _, err2 := r.Seek(0, io.SeekStart); err2 == nil {
			_, _ = io.Copy(tmp, r)
		}
		tmp.Close()
		src = tmp.Name()
		tmpFile = tmp.Name()
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
		title := "Video: " + filepath.Base(info.Path)
		w := a.NewWindow(title)
		v := mpvplayer.NewVideo(player)
		w.SetCloseIntercept(func() {
			v.Close()
			w.Close()
			if tmpFile != "" {
				os.Remove(tmpFile)
			}
		})
		w.SetContent(v)
		w.Resize(fyne.NewSize(800, 520))
		w.Show()
	})
}
