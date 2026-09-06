package tieconfig

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/conf"
	"github.com/uidbz/tie/client"
)

func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	if c, ok := o.(*fyne.Container); ok {
		for _, ch := range c.Objects {
			walk(ch, fn)
		}
	}
	if s, ok := o.(*container.Scroll); ok {
		walk(s.Content, fn)
	}
}

// twoCollectionConfig returns a config with "images" and "audio" collections
// on distinct triplestores, defaulting to "images".
func twoCollectionConfig() client.Config {
	return client.Config{
		Namespace:        "Collections",
		TripleStoreURL:   "http://images:1161",
		DefaultFileHosts: []string{"default"},
		FileHosts: map[string]client.FileHost{
			"default": {URL: "http://images:1162"},
		},
		DefaultCollection: "images",
		Collections: map[string]client.CollectionEntry{
			"images": {Namespace: "Collections", Collection: "images", FileHosts: []string{"default"}},
			"audio":  {Namespace: "Collections", Collection: "audio", TripleStoreURL: "http://audio:1161", FileHosts: []string{"default"}},
		},
	}
}

func TestAppCollection(t *testing.T) {
	cfg := twoCollectionConfig()
	for _, tc := range []struct {
		name       string
		stored     string
		appDefault string
		want       string
	}{
		{"stored entry wins", "audio", "images", "audio"},
		{"stale stored falls back to app default", "gone", "audio", "audio"},
		{"no stored binds the app's own profile", "", "audio", "audio"},
		{"no app default entry falls back to file default", "", "videos", "images"},
		{"nothing resolves to file default", "", "", "images"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AppCollection(cfg, tc.stored, tc.appDefault); got != tc.want {
				t.Fatalf("AppCollection(%q, %q) = %q, want %q", tc.stored, tc.appDefault, got, tc.want)
			}
		})
	}
}

// TestEditorSelectFiresOnSelect verifies that picking a collection in the
// dropdown fires onSelect (so the app can switch its client immediately)
// without writing the tie file.
func TestEditorSelectFiresOnSelect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := conf.WriteConfig(path, twoCollectionConfig()); err != nil {
		t.Fatal(err)
	}
	loaded, err := client.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	var selected []string
	editor := Editor(loaded, path, "images", func(name string) { selected = append(selected, name) }, nil)

	test.NewApp()
	w := test.NewWindow(editor)
	defer w.Close()

	var dropdown *widget.Select
	walk(editor, func(o fyne.CanvasObject) {
		if s, ok := o.(*widget.Select); ok {
			dropdown = s
		}
	})
	if dropdown == nil {
		t.Fatal("dropdown not found")
	}
	if dropdown.Selected != "images" {
		t.Fatalf("preselected = %q, want images (the active collection)", dropdown.Selected)
	}

	dropdown.SetSelected("audio") // a user pick fires OnChanged
	if len(selected) != 1 || selected[0] != "audio" {
		t.Fatalf("onSelect calls = %v, want [audio]", selected)
	}

	// No file write on select: the disk config still defaults to images.
	reloaded, err := client.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultCollection != "images" {
		t.Fatalf("file DefaultCollection = %q after select, want images (no write)", reloaded.DefaultCollection)
	}
}

// TestEditorApplySwitchesCollection verifies Apply writes the applied
// collection to the tie file (as DefaultCollection) without double-firing
// onSelect, and that a client built from the saved config binds it.
func TestEditorApplySwitchesCollection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := conf.WriteConfig(path, twoCollectionConfig()); err != nil {
		t.Fatal(err)
	}
	loaded, err := client.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	var selected []string
	var saved client.Config
	applied := false
	editor := Editor(loaded, path, "images",
		func(name string) { selected = append(selected, name) },
		func(c client.Config) { saved = c; applied = true })

	test.NewApp()
	w := test.NewWindow(editor)
	defer w.Close()

	var dropdown *widget.Select
	var applyBtn *widget.Button
	walk(editor, func(o fyne.CanvasObject) {
		switch w := o.(type) {
		case *widget.Select:
			dropdown = w
		case *widget.Button:
			if w.Text == "Apply connection" {
				applyBtn = w
			}
		}
	})
	if dropdown == nil || applyBtn == nil {
		t.Fatalf("widgets not found: dropdown=%v apply=%v", dropdown, applyBtn)
	}

	dropdown.SetSelected("audio")
	applyBtn.OnTapped()
	if !applied {
		t.Fatal("onApply never fired")
	}
	if len(selected) != 1 {
		t.Fatalf("onSelect fired %v; Apply's internal SetSelected must not re-fire", selected)
	}
	if saved.DefaultCollection != "audio" {
		t.Fatalf("saved.DefaultCollection = %q, want audio", saved.DefaultCollection)
	}

	// The written file must carry the switch so a restart keeps it.
	reloaded, err := client.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultCollection != "audio" {
		t.Fatalf("reloaded DefaultCollection = %q, want audio", reloaded.DefaultCollection)
	}
	if _, ok := reloaded.Collections["images"]; !ok {
		t.Fatal("reloaded config lost the images collection")
	}

	// The live client must bind the audio collection's triplestore.
	tc := client.NewTieClientFor(saved, saved.DefaultCollection)
	if info := tc.CollectionInfo(); info.CollectionId != "audio" {
		t.Fatalf("client bound collection = %q, want audio", info.CollectionId)
	}
}
