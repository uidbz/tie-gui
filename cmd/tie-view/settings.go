package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
)

// makeSettingsTab holds two sub-tabs: the shared tie-config editor
// ("Connection") and the quick tag bar editor ("Quick tags"). Picking a
// collection in the editor's dropdown or applying the form runs
// onSwitchCollection, which rebinds the live client to that collection and
// remembers it as this app's own profile (only Apply writes the tie file);
// onApply then runs (e.g. to reload the tag list).
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
	)
	return container.NewTabItem("Settings", container.NewBorder(nil, nil, nil, nil, tabs))
}
