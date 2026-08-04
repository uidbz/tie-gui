package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"

	"git.sr.ht/~uid/conf"
	"git.sr.ht/~uid/tie/client"

	"git.sr.ht/~uid/imgview/gallery"
	"git.sr.ht/~uid/imgview/tagselection"
	// "github.com/pkg/profile"
)

//go:embed Icon.png
var icon []byte

func main() {
	// defer profile.Start(profile.CPUProfile).Stop()
	// defer profile.Start().Stop()
	tieTag := flag.String("tag", "favorite", "Show images with tag")
	tieConfigName := flag.String("config", "", "Tie config file to load: a name searched in tie's config dirs (like `tie -c`), or a file path (default: config.toml from the user config dir)")
	flag.StringVar(tieConfigName, "c", "", "Shorthand for -config")
	flag.StringVar(&tieHostName, "host", "", "Fetch content from this filehost named in the tie config (default: \"fast\" when configured, else the first DefaultFileHosts entry)")
	flag.Parse()

	// Append .toml extension when the caller omitted it.
	if *tieConfigName != "" && !strings.HasSuffix(*tieConfigName, ".toml") {
		*tieConfigName += ".toml"
	}

	myApp := app.NewWithID("sr.ht.uid.imgview")
	myApp.SetIcon(fyne.NewStaticResource("icon", icon))
	myWindow := myApp.NewWindow("imgview")

	config := gallery.LoadConfig(myWindow, "")

	tieConfig := loadTieConfig(*tieConfigName)
	if tieHostName != "" {
		if _, ok := tieConfig.FileHosts[tieHostName]; !ok {
			fmt.Fprintf(os.Stderr, "No filehost %q in the tie config. Configured filehosts: %s\n", tieHostName, strings.Join(fileHostNames(tieConfig), ", "))
			os.Exit(2)
		}
	}
	tieClient := client.NewTieClient(tieConfig)

	viewer := gallery.NewViewer(myApp, myWindow, config, func(t *gallery.Tile) {
		t.Viewer.ChangeImage(t.Info)
	})
	viewer.Thumbnailer = &filehostThumbnailer{tie: tieClient, tileWidth: int(config.General.TileWidth)}
	fsTree := newTieFSTree(viewer, tieClient)
	browseDir := func(uid client.DirUID) { fsTree.showDirUID(uid, "") }
	viewer.Sidebar = makeSidebar(myWindow, viewer, tieClient, fsTree, browseDir)

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	readFromTie(viewer, tieClient, []string{*tieTag}, nil, "tag", browseDir)
	// readFromTie(viewer, tieClient, []string{"4"}, nil, "rating", browseDir)

	viewer.OnImageChange = func(info *gallery.ImageInfo) {
		myWindow.Canvas().Focus(viewer.CurrentImageView)
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
func makeSidebar(window fyne.Window, viewer *gallery.Viewer, tc *client.TieClient, fsTree *tieFSTree, browseDir func(client.DirUID)) *container.AppTabs {
	return container.NewAppTabs(
		container.NewTabItem("Tags", makeTagSidebar(window, viewer, tc, browseDir)),
		container.NewTabItem("Files", fsTree.tree),
	)
}

// makeTagSidebar builds the tag sidebar: it lists the tags registered under
// the "tags" key and re-queries the gallery whenever the selection changes.
func makeTagSidebar(window fyne.Window, viewer *gallery.Viewer, tc *client.TieClient, browseDir func(client.DirUID)) *tagselection.TagSelection {
	ts := tagselection.NewTagSelection(window)
	go func() {
		row, err := tc.Get("tags")
		if err != nil {
			fmt.Println("Error getting tags:", err)
			return
		}
		fyne.Do(func() {
			all := client.RowValues(row, "all")
			for _, tag := range all {
				ts.AddTag(tag)
			}
			favorites := client.RowValues(row, "favorite")
			if len(favorites) == 0 {
				// No favorites configured: list every tag so the sidebar
				// isn't empty until something is typed in the search box.
				favorites = all
				ts.SetListLabel("All tags")
			}
			for _, tag := range favorites {
				ts.AddFavorite(tag)
			}
		})
	}()
	ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		readFromTie(viewer, tc, in, ex, "tag", browseDir)
		viewer.ChangeGallery()
	}

	return ts
}
