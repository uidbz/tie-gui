package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
)

// browseViewTabs is the desktop wall's view switcher: Covers, Table and Now
// Playing. It is a selector only (the content lives below it), so it can wrap
// any content the browse page or the gallery pushes to the window without the
// tabs owning that content.
type browseViewTabs struct {
	object  fyne.CanvasObject
	buttons map[string]*widget.Button
}

func newBrowseViewTabs(onSelect func(string)) *browseViewTabs {
	t := &browseViewTabs{buttons: make(map[string]*widget.Button)}
	add := func(view, label string, icon fyne.Resource) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, func() { onSelect(view) })
		b.Importance = widget.LowImportance
		t.buttons[view] = b
		return b
	}
	row := container.NewHBox(
		add(config.BrowseCovers, "Covers", theme.GridIcon()),
		add(config.BrowseTable, "Table", theme.ListIcon()),
		add(config.BrowseNowPlaying, "Now Playing", theme.MediaPlayIcon()),
	)
	t.object = container.NewBorder(nil, widget.NewSeparator(), nil, nil,
		container.NewPadded(row))
	return t
}

// setActive highlights the tab matching view.
func (t *browseViewTabs) setActive(view string) {
	if view != config.BrowseCovers && view != config.BrowseTable && view != config.BrowseNowPlaying {
		view = config.BrowseCovers
	}
	for v, b := range t.buttons {
		importance := widget.LowImportance
		if v == view {
			importance = widget.HighImportance
		}
		if b.Importance != importance {
			b.Importance = importance
			b.Refresh()
		}
	}
}
