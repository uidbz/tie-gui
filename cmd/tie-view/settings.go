package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
)

// makeSettingsTab holds three sub-tabs: the shared tie-config editor
// ("Connection"), the quick tag bar editor ("Quick tags") and the startup
// page chooser ("Startup"). Picking a collection in the editor's dropdown or
// applying the form runs onSwitchCollection, which rebinds the live client to
// that collection and remembers it as this app's own profile (only Apply
// writes the tie file); onApply then runs (e.g. to reload the tag list).
func makeSettingsTab(tc *client.TieClient, activeCollection func() string, onSwitchCollection func(client.Config, string), onApply func(), quickEditor fyne.CanvasObject) *container.TabItem {
	editor := tieconfig.Editor(tc.Config, tieConfigPath, activeCollection(),
		func(name string) {
			onSwitchCollection(tc.Config, name)
			if onApply != nil {
				onApply()
			}
		},
		func(saved client.Config) {
			onSwitchCollection(saved, saved.DefaultCollection)
			if onApply != nil {
				onApply()
			}
		})
	// AppTabs' MinSize is the max of its tab contents' MinSizes, and both
	// editors are scroll containers — whose MinSize is only the small scroll
	// minimum (~32px). In the narrow sidebar that makes the tabs report a
	// tiny height, so the tab content collapses and only the editor's
	// dropdown row is visible. Anchoring the tabs in a border center gives
	// them a stable frame to fill instead.
	tabs := container.NewAppTabs(
		container.NewTabItem("Connection", editor),
		container.NewTabItem("Quick tags", quickEditor),
		makeStartupTab(fyne.CurrentApp()),
	)
	return container.NewTabItem("Settings", container.NewBorder(nil, nil, nil, nil, tabs))
}

// Startup page Preferences: what the gallery shows at launch on the desktop
// (a tie: URL argument and the mobile DCIM view take precedence over it).
const (
	prefStartupPage = "startup.page"
	prefStartupTag  = "startup.tag"
)

// Startup page values for prefStartupPage.
const (
	startupFavorites = "favorites" // images tagged "favorite"
	startupLatest    = "latest"    // every image, most recently imported first
	startupTag       = "tag"       // images tagged prefStartupTag
	startupNone      = "none"      // blank gallery
)

// makeStartupTab builds the Settings tab choosing the startup page. Changes
// write Preferences immediately and apply at the next launch.
func makeStartupTab(app fyne.App) *container.TabItem {
	pages := []string{
		"Favorites (tag: favorite)",
		"Latest images",
		"A tag…",
		"Blank gallery",
	}
	values := []string{startupFavorites, startupLatest, startupTag, startupNone}

	sel := widget.NewSelect(pages, nil)
	sel.OnChanged = func(label string) {
		for i, text := range pages {
			if text == label {
				app.Preferences().SetString(prefStartupPage, values[i])
				return
			}
		}
	}
	// Set the field directly so OnChanged does not fire during construction.
	sel.Selected = pages[0]
	current := app.Preferences().StringWithFallback(prefStartupPage, startupFavorites)
	for i, v := range values {
		if v == current {
			sel.Selected = pages[i]
		}
	}

	tagEntry := widget.NewEntry()
	tagEntry.SetText(app.Preferences().String(prefStartupTag))
	tagEntry.SetPlaceHolder(`(tag shown when the page is "A tag…")`)
	tagEntry.OnChanged = func(s string) {
		app.Preferences().SetString(prefStartupTag, s)
	}

	note := widget.NewLabel("Applies at the next launch. A tie: URL argument and " +
		"the mobile camera view take precedence over the startup page.")
	note.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Startup page", sel),
		widget.NewFormItem("Startup tag", tagEntry),
	)
	return container.NewTabItem("Startup", container.NewVBox(form, note))
}
