package main

import (
	_ "embed"
	"flag"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

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
	flag.Parse()

	myApp := app.NewWithID("sr.ht.uid.imgview")
	myApp.SetIcon(fyne.NewStaticResource("icon", icon))
	myWindow := myApp.NewWindow("imgview")

	config := gallery.LoadConfig(myWindow)

	// Load from the user config dir only: client.LoadConfig also searches
	// the current directory, where any stray config.toml (e.g. imgview's
	// own) would shadow the tie config.
	tieConfig := client.Config{}
	if _, err := conf.LoadFromUserConfigDir("tie", "config.toml", &tieConfig); err != nil {
		fmt.Println("Error reading tie config:", err)
		tieConfig = client.DefaultConfig()
	}
	tieClient := client.NewTieClient(tieConfig)

	viewer := gallery.NewViewer(myApp, myWindow, config, func(t *gallery.Tile) {
		t.Viewer.ChangeImage(t.Info)
	})
	viewer.Thumbnailer = &filehostThumbnailer{tie: tieClient, tileWidth: int(config.General.TileWidth)}
	viewer.Sidebar = makeTagSidebar(myWindow, viewer, tieClient)

	viewer.Init()
	myWindow.Canvas().SetOnTypedKey(viewer.KeyPress)

	readFromTie(viewer, tieClient, []string{*tieTag}, nil, "tag")
	// readFromTie(viewer, tieClient, []string{"4"}, nil, "rating")

	viewer.OnImageChange = func(info *gallery.ImageInfo) {
		myWindow.Canvas().Focus(viewer.CurrentImageView)
	}

	myWindow.SetContent(viewer.Content)
	viewer.LoadGallery()
	viewer.CreateView()

	myWindow.Resize(fyne.NewSize(config.General.DefaultWidth, config.General.DefaultHeight))

	myWindow.ShowAndRun()
}

// makeTagSidebar builds the tag sidebar: it lists the tags registered under
// the "tags" key and re-queries the gallery whenever the selection changes.
func makeTagSidebar(window fyne.Window, viewer *gallery.Viewer, tc *client.TieClient) *tagselection.TagSelection {
	ts := tagselection.NewTagSelection(window)
	go func() {
		row, err := tc.Get("tags")
		if err != nil {
			fmt.Println("Error getting tags:", err)
			return
		}
		fyne.Do(func() {
			for _, tag := range client.RowValues(row, "all") {
				ts.AddTag(tag)
			}
			for _, tag := range client.RowValues(row, "favorite") {
				ts.AddFavorite(tag)
			}
		})
	}()
	ts.OnSelectedChanged = func() {
		in, ex := ts.SelectedTags()
		readFromTie(viewer, tc, in, ex, "tag")
		viewer.ChangeGallery()
	}

	return ts
}
