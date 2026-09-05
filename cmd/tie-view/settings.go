package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
)

// makeSettingsTab holds two sub-tabs: the shared tie-config editor
// ("Connection") and the quick tag bar editor ("Quick tags"). Applying the
// connection writes the config to tieConfigPath and rebuilds the live client
// on the selected collection, then runs onApply (e.g. to reload the tag list).
func makeSettingsTab(tc *client.TieClient, onApply func(), quickEditor fyne.CanvasObject) *container.TabItem {
	editor := tieconfig.Editor(tc.Config, tieConfigPath, func(saved client.Config) {
		*tc = *client.NewTieClientFor(saved, saved.DefaultCollection)
		if onApply != nil {
			onApply()
		}
	})
	tabs := container.NewAppTabs(
		container.NewTabItem("Connection", editor),
		container.NewTabItem("Quick tags", quickEditor),
	)
	return container.NewTabItem("Settings", tabs)
}
