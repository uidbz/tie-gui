package main

import (
	"fyne.io/fyne/v2/container"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/tieconfig"
)

// makeSettingsTab wraps the shared tie-config editor in a tab. Apply writes the
// config to tieConfigPath and rebuilds the live client on the selected
// collection, then runs onApply (e.g. to reload the tag list).
func makeSettingsTab(tc *client.TieClient, onApply func()) *container.TabItem {
	editor := tieconfig.Editor(tc.Config, tieConfigPath, func(saved client.Config) {
		*tc = *client.NewTieClientFor(saved, saved.DefaultCollection)
		if onApply != nil {
			onApply()
		}
	})
	return container.NewTabItem("Settings", editor)
}
