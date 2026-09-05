package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/gallery"
	"github.com/uidbz/tie-gui/mpvplayer"
	"github.com/uidbz/tie-gui/tagselection"
	"github.com/uidbz/tie-gui/tieconfig"
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

	// Adjust config for mobile if needed
	platform := gallery.NewPlatform()
	if platform.IsMobile() {
		config.AdjustForMobile()
	}

	tieConfigPath = tieconfig.ResolvePath(*tieConfigName)
	tieConfig := tieconfig.Load(*tieConfigName)
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

	// Quick tagging mode: a strip of icon buttons over the image edge that
	// toggles configured tags with one tap or key. Its configuration lives in
	// quicktags.toml (edited by hand or via Settings → Quick tags) as a
	// default set plus per-collection overrides; whether the mode is on
	// persists in Preferences across sessions.
	quickCfgPath := quickTagConfigPath()
	quickCfg := loadQuickTagConfig(quickCfgPath)
	// activeCollection names the tie collection the live client is bound to
	// (the settings editor sets DefaultCollection to the applied one).
	activeCollection := func() string { return tieClient.Config.DefaultCollection }
	quickBar := newQuickTagBar(tieClient, quickCfg.For(activeCollection()), filepath.Dir(quickCfgPath), platform.IsMobile())
	const quickPref = "quicktag.enabled"
	quickEnabled := myApp.Preferences().BoolWithFallback(quickPref, false)
	// The bar and the tag panel edit the same image; keep them in step.
	tagger.OnTagsChanged = quickBar.SetTags
	quickBar.OnTagsChanged = tagger.SetTags

	viewer := gallery.NewGallery(myApp, myWindow, config, func(t *gallery.Tile) {
		if t.Info.InputIsVideo {
			go openTieVideo(t.Viewer, t.Info)
			return
		}
		t.Viewer.ChangeImage(t.Info)
	})
	viewer.Thumbnailer = &filehostThumbnailer{tie: tieClient, tileWidth: int(config.General.TileWidth)}
	toggleTagger := func() {
		tagger.Toggle(tagger.hash)
		viewer.Content.Refresh()
	}
	if viewer.Platform().ShouldUseTapForAction() {
		// On desktop a single tap on the image opens/closes the tag panel.
		viewer.OnTapped = toggleTagger
	} else {
		// On mobile a tap is used for other interactions (focus, navigation).
		// Use a swipe-up gesture instead to open/close the tag panel.
		viewer.OnSwipeUp = toggleTagger
	}
	// Restore keyboard focus to the image view when the panel is closed so
	// that hotkeys (next/prev, zoom, etc.) keep working on desktop.
	tagger.OnHide = func() {
		if viewer.Platform().ShouldFocusImageView() {
			myWindow.Canvas().Focus(viewer.CurrentImageView)
		}
	}
	fsTree := newTieFSTree(viewer, tieClient)

	// curReader is the tie entry behind the displayed image (nil for none or
	// non-tie content), tracked by OnImageChange for the quick tag bar.
	var curReader *tieReader
	// syncQuickOverlay adds or removes the bar on the live image view so
	// toggling the mode takes effect without navigating.
	syncQuickOverlay := func() {
		if !viewer.ImageViewActive() {
			return
		}
		objs := viewer.Content.Objects
		idx := slices.Index(objs, fyne.CanvasObject(quickBar.Overlay))
		switch {
		case quickEnabled && idx < 0:
			viewer.Content.Objects = append(objs, quickBar.Overlay)
		case !quickEnabled && idx >= 0:
			viewer.Content.Objects = slices.Delete(objs, idx, idx+1)
		default:
			return
		}
		viewer.Content.Refresh()
	}
	setQuickTagging := func(on bool) {
		quickEnabled = on
		myApp.Preferences().SetBool(quickPref, on)
		if on {
			quickBar.SetImage(curReader)
		}
		syncQuickOverlay()
	}
	// Bar shortcuts resolve through quickKeys at press time, so a settings
	// change only has to register key names not seen before (RegisterHotkey
	// bindings cannot be removed; duplicates would toggle twice).
	quickKeys := quickBar.Keys()
	quickKeysBound := map[fyne.KeyName]bool{}
	bindQuickKeys := func() {
		for name := range quickKeys {
			if quickKeysBound[name] {
				continue
			}
			quickKeysBound[name] = true
			name := name
			viewer.RegisterHotkey(name, func() {
				if fn := quickKeys[name]; fn != nil && quickEnabled && viewer.ImageViewActive() {
					fn()
				}
			})
		}
	}
	// applyQuickTagConfig installs cfg and shows the active collection's set;
	// also run when the active collection changes so the bar follows it.
	applyQuickTagConfig := func(cfg quickTagConfig) {
		quickCfg = cfg
		quickBar.Rebuild(quickCfg.For(activeCollection()))
		quickKeys = quickBar.Keys()
		bindQuickKeys()
		if quickEnabled && viewer.ImageViewActive() {
			viewer.Content.Refresh()
		}
	}

	viewer.MenuItems = func() []*fyne.MenuItem {
		label := "Show hidden directories"
		if fsTree.showHidden {
			label = "Hide hidden directories"
		}
		quickLabel := "Quick tagging mode"
		if quickEnabled {
			quickLabel = "Quick tagging mode ✓"
		}
		return []*fyne.MenuItem{
			fyne.NewMenuItem(quickLabel, func() { setQuickTagging(!quickEnabled) }),
			fyne.NewMenuItem(label, func() {
				fsTree.SetShowHidden(!fsTree.showHidden)
			}),
		}
	}
	browseDir := func(uid client.DirUID) { fsTree.showDirUID(uid, "") }
	quickEditor, refreshQuickEditor := makeQuickTagEditor(myWindow, quickCfgPath, quickCfg,
		func() ([]string, string) { return collectionNames(tieClient.Config), activeCollection() },
		applyQuickTagConfig)
	// After a connection change the bar switches to the new collection's set
	// and the editor follows.
	onCollectionChanged := func() {
		applyQuickTagConfig(quickCfg)
		refreshQuickEditor()
	}
	viewer.Sidebar = makeSidebar(myWindow, viewer, tieClient, fsTree, browseDir, tagger, quickEditor, onCollectionChanged)

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)
	// Hotkeys must be registered after Init, which resets the binding list.
	for _, k := range config.Image.ShowTagbar {
		viewer.RegisterHotkey(k, func() {
			if viewer.ImageViewActive() {
				setQuickTagging(!quickEnabled)
			}
		})
	}
	bindQuickKeys()

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

	// On mobile, load the default image directory (DCIM/Camera). On desktop,
	// load images by tag (default "favorite") to populate the gallery with
	// quick-access content.
	if viewer.Platform().IsMobile() {
		// Try /DCIM/Camera first (typical Android camera directory), then /DCIM,
		// then fall back to root if neither exists. DirUIDFromPath and showDir
		// are network calls, so they must not run on the main thread: with a
		// dead server the timeout-less tie HTTP client would hang startup
		// (ANR) for minutes. ChangeGallery below runs via fyne.Do once the
		// listing is in.
		go func() {
			dir := "/DCIM/Camera"
			if uid, err := tieClient.DirUIDFromPath(dir); err != nil || uid == "" {
				dir = "/DCIM"
				if uid, err := tieClient.DirUIDFromPath(dir); err != nil || uid == "" {
					dir = "/"
				}
			}
			fsTree.showDir(dir, "")
			fyne.Do(viewer.ChangeGallery)
		}()
	} else {
		readFromTie(viewer, tieClient, []string{*tieTag}, nil, "tag", browseDir)
		// readFromTie(viewer, tieClient, []string{"4"}, nil, "rating", browseDir)
	}

	viewer.OnImageChange = func(info *gallery.ImageInfo) {
		gallery.FocusImageViewOnDesktop(myWindow, viewer)
		curReader, _ = info.CustomReader.(*tieReader)
		// Overlay the tag panel on top of the image view. ChangeImage already
		// set Content.Objects to [CurrentImage]; append the taggerOverlay so
		// the panel can be toggled without rebuilding the content stack. The
		// quick tag bar goes underneath it so an open panel covers the bar.
		if quickEnabled {
			viewer.Content.Objects = append(viewer.Content.Objects, quickBar.Overlay)
			quickBar.SetImage(curReader)
		}
		viewer.Content.Objects = append(viewer.Content.Objects, taggerOverlay)
		viewer.Content.Refresh()
		// Track which image is displayed so Toggle opens the correct panel.
		if curReader != nil {
			tagger.SetCurrentHash(curReader.hash)
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

// tieConfigPath is the resolved path the tie config was loaded from and is
// saved back to by the settings tab. Set once in main.
var tieConfigPath string

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
// by tag, the second navigates the tie virtual filesystem, the third holds
// the connection and quick tag settings. onCollectionChanged runs after a
// connection change is applied, once the tag list reload has been kicked off.
func makeSidebar(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID), tagger *imageTagger, quickEditor fyne.CanvasObject, onCollectionChanged func()) *container.AppTabs {
	tagWidget, reloadTags := makeTagSidebar(window, viewer, tc, browseDir, tagger)
	onApply := func() {
		reloadTags()
		if onCollectionChanged != nil {
			onCollectionChanged()
		}
	}
	return container.NewAppTabs(
		container.NewTabItem("Tags", tagWidget),
		container.NewTabItem("Files", fsTree.tree),
		makeSettingsTab(tc, onApply, quickEditor),
	)
}

// collectionNames returns the sorted collection names of a tie config.
func collectionNames(c client.Config) []string {
	names := make([]string, 0, len(c.Collections))
	for name := range c.Collections {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ratingFilterOptions maps the rating Select's labels to filters. "Any" clears
// the constraint; "N" is exact; "N+" is at least N; "Unrated" uses the
// server-side MissingRelation predicate.
var ratingFilterOptions = []struct {
	label  string
	filter ratingFilter
}{
	{"Any", ratingFilter{mode: ratingAny}},
	{"Unrated", ratingFilter{mode: ratingUnrated}},
	{"5", ratingFilter{mode: ratingExact, n: 5}},
	{"4", ratingFilter{mode: ratingExact, n: 4}},
	{"3", ratingFilter{mode: ratingExact, n: 3}},
	{"2", ratingFilter{mode: ratingExact, n: 2}},
	{"1", ratingFilter{mode: ratingExact, n: 1}},
	{"4+", ratingFilter{mode: ratingMin, n: 4}},
	{"3+", ratingFilter{mode: ratingMin, n: 3}},
	{"2+", ratingFilter{mode: ratingMin, n: 2}},
	{"1+", ratingFilter{mode: ratingMin, n: 1}},
}

// sortModeOptions maps the sort Select's labels to sort modes.
var sortModeOptions = []struct {
	label string
	mode  sortMode
}{
	{"Default", sortDefault},
	{"Name ↑", sortNameAsc},
	{"Name ↓", sortNameDesc},
	{"Rating ↓", sortRatingDesc},
	{"Rating ↑", sortRatingAsc},
	{"Newest", sortNewest},
	{"Oldest", sortOldest},
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
func makeTagSidebar(window fyne.Window, viewer *gallery.Gallery, tc *client.TieClient, browseDir func(client.DirUID), tagger *imageTagger) (fyne.CanvasObject, func()) {
	ts := tagselection.NewTagSelection(window)
	// Sidebar uses include/exclude filtering, so show the checkbox.
	ts.ShowIncludeExclude = true

	// Current filter/sort state, all read and written on the UI goroutine.
	curRating := ratingFilter{mode: ratingAny}
	curSort := sortDefault
	untaggedOnly := false

	// refreshGallery re-queries the gallery from the live tag selection plus the
	// rating/sort/untagged controls. Used by every control's change handler.
	refreshGallery := func() {
		in, ex := ts.SelectedTags()
		queryGallery(viewer, tc, galleryFilter{
			include:      in,
			exclude:      ex,
			rating:       curRating,
			untaggedOnly: untaggedOnly,
			sort:         curSort,
		}, browseDir)
		viewer.ChangeGallery()
	}

	ratingSelect := widget.NewSelect(nil, func(label string) {
		for _, o := range ratingFilterOptions {
			if o.label == label {
				curRating = o.filter
				break
			}
		}
		refreshGallery()
	})
	for _, o := range ratingFilterOptions {
		ratingSelect.Options = append(ratingSelect.Options, o.label)
	}
	// Set the field directly (not SetSelectedIndex) so OnChanged does not fire
	// during construction — the sidebar is built before viewer.Init(), so an
	// early refreshGallery/ChangeGallery would run against an uninitialized view.
	ratingSelect.Selected = ratingFilterOptions[0].label // "Any"

	sortSelect := widget.NewSelect(nil, func(label string) {
		for _, o := range sortModeOptions {
			if o.label == label {
				curSort = o.mode
				break
			}
		}
		refreshGallery()
	})
	for _, o := range sortModeOptions {
		sortSelect.Options = append(sortSelect.Options, o.label)
	}
	sortSelect.Selected = sortModeOptions[0].label // "Default"

	// The Untagged toggle shows images that carry no tag at all (server-side
	// MissingRelation="tag"). Enabling it clears the tag selection; selecting a
	// tag later turns it back off (handled in OnSelectedChanged).
	var untaggedBtn *widget.Button
	untaggedBtn = widget.NewButton("Untagged", func() {
		untaggedOnly = !untaggedOnly
		if untaggedOnly {
			untaggedBtn.SetText("Untagged ✓")
			ts.ClearSelected() // fires OnSelectedChanged → would clear the flag
			untaggedOnly = true
		} else {
			untaggedBtn.SetText("Untagged")
		}
		refreshGallery()
	})

	controls := container.NewVBox(
		container.NewGridWithColumns(2,
			container.NewBorder(nil, nil, widget.NewLabel("Rating"), nil, ratingSelect),
			container.NewBorder(nil, nil, widget.NewLabel("Sort"), nil, sortSelect),
		),
		untaggedBtn,
	)

	// allTags and allFavorites hold the full unfiltered lists from the most
	// recent tag fetch. All reads and writes happen on the UI goroutine
	// (inside fyne.Do), so no mutex is needed.
	var allTags []string
	var allFavorites []string
	var allFavoritesLabel string

	// Tags added to images via the tagger are registered in tie's
	// "tags"/"all" index by syncTags, which then fires OnTagsAdded on the UI
	// goroutine. Add genuinely new tags to the sidebar's search trie (and
	// the full-list snapshot) without a reload — reloadTags would clear the
	// user's current search selection.
	if tagger != nil {
		tagger.OnTagsAdded = func(tags []string) {
			changed := false
			for _, tag := range tags {
				if !slices.Contains(allTags, tag) {
					allTags = append(allTags, tag)
					changed = true
				}
				ts.AddTag(tag)
			}
			// In the "no favorites configured" fallback the quick-pick list
			// shows every tag; refresh it to include the new arrivals.
			if changed && allFavoritesLabel == "All tags" {
				ts.SetFavorites(allTags)
			}
		}
	}

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
			row, err := tc.Get(client.TieTags.String())
			if err != nil {
				fmt.Println("Error getting tags:", err)
				fyne.Do(func() { ts.SetListLabel("Error loading tags") })
				return
			}
			fyne.Do(func() {
				allTags = client.RowValues(row, client.TieAll.String())
				for _, tag := range allTags {
					ts.AddTag(tag)
				}
				// Capture the actual tie favorites before the sidebar fallback
				// so the tagger's ☆/★ state reflects the real relation, not the
				// "show everything" substitute used when no favorites are configured.
				actualFavorites := client.RowValues(row, client.TieFavorite.String())
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
		// Clicking a tag row or include/exclude checkbox moves keyboard
		// focus to that widget, and typed keys would no longer reach the
		// window-level gallery hotkeys. Release it.
		window.Canvas().Unfocus()
		// Selecting any tag exits untagged-only mode; an empty selection (e.g.
		// from ClearSelected when entering untagged mode) leaves it untouched.
		if (len(in) > 0 || len(ex) > 0) && untaggedOnly {
			untaggedOnly = false
			untaggedBtn.SetText("Untagged")
		}
		refreshGallery()

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

	sidebar := container.NewBorder(controls, nil, nil, nil, ts)
	return sidebar, reloadTags
}

// openTieVideo opens a libmpv video player window for a tie video entry.
// It streams directly from the filehost URL when available; otherwise it
// falls through to downloading the content to a temporary file.
func openTieVideo(viewer *gallery.Gallery, info *gallery.ImageInfo) {
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
		var onClose func()
		if tmpFile != "" {
			onClose = func() { os.Remove(tmpFile) }
		}
		viewer.ShowVideo(player, info.Path, onClose)
	})
}
