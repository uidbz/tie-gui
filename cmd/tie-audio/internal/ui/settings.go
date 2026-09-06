package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	tieclient "github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/tieconfig"
)

func (a *App) buildSettings() fyne.CanvasObject {
	server := widget.NewEntry()
	server.SetText(a.session.Cfg.PwplayServer)
	server.SetPlaceHolder("http://host:8080")

	tieCfg := widget.NewEntry()
	tieCfg.SetText(a.session.Cfg.TieConfig)
	tieCfg.SetPlaceHolder("(blank = tie default config)")

	fileHost := widget.NewEntry()
	fileHost.SetText(a.session.Cfg.FileHost)
	fileHost.SetPlaceHolder("(blank = tie default filehost)")

	current := func() config.AppConfig {
		return config.AppConfig{
			PwplayServer: server.Text,
			TieConfig:    tieCfg.Text,
			FileHost:     fileHost.Text,
			// Preserve the collection selection and column customization: the
			// collection is picked via the connection editor below and columns
			// from the album/queue views; rebuilding from scratch would drop both.
			TieCollection: a.session.Cfg.TieCollection,
			AlbumColumns:  a.session.Cfg.AlbumColumns,
			QueueColumns:  a.session.Cfg.QueueColumns,
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("pwplay server", server),
		widget.NewFormItem("tie config", tieCfg),
		widget.NewFormItem("filehost", fileHost),
	)

	save := widget.NewButton("Save", func() {
		cfg := current()
		if err := config.Save(cfg); err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		a.session = data.NewSession(cfg)
		a.browse.session = a.session
		a.browse.loadTags()
		dialog.ShowInformation("Saved", "Settings saved.", a.win)
	})

	test := widget.NewButton("Test pwplay connection", func() {
		s := data.NewSession(current())
		if err := s.PingPwplay(); err != nil {
			dialog.ShowError(err, a.win)
			return
		}
		dialog.ShowInformation("Connected", "pwplay server reachable.", a.win)
	})

	back := widget.NewButtonWithIcon("Back", theme.NavigateBackIcon(), func() {
		a.browse.showBrowse()
	})

	header := container.NewVBox(
		container.NewHBox(back),
		widget.NewLabelWithStyle("Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
	)

	// Connection editor: edit the tie config's [Collections.*] entries as
	// TOML in-app (matching tie-view's Settings tab), so a connection or
	// collection can be set up comfortably on Android. Picking a collection in
	// the dropdown switches this app to it immediately and remembers the
	// choice in tie-audio's own config — the tie file's shared
	// DefaultCollection is only touched by Apply, so the apps don't fight
	// over it. Both paths swap the client in place (the tie-view
	// struct-overwrite pattern), so every existing *TieClient holder (browse
	// page, queue page) sees the new collection, then reload the tag sidebar
	// and clear the album wall (stale albums from the prior collection can
	// neither display nor be opened).
	switchCollection := func(name string) {
		a.session.Cfg.TieCollection = name
		if err := config.Save(a.session.Cfg); err != nil {
			dialog.ShowError(err, a.win)
		}
		a.browse.loadTags()
		a.browse.clearAlbums()
	}
	connEditor := tieconfig.Editor(a.session.Tie.Config, tieconfig.ResolvePath(a.session.Cfg.TieConfig),
		a.session.Collection,
		func(name string) {
			a.session.SetCollection(name)
			switchCollection(name)
		},
		func(saved tieclient.Config) {
			a.session.SetTieConfig(saved)
			switchCollection(a.session.Collection)
		})

	// The connection editor is the border's center, so it gets all remaining
	// space. Nesting it in the form's scroll VBox instead would collapse it:
	// the editor is itself a scroll container, whose MinSize is only the
	// small scroll minimum (~32px), so the VBox handed it just that — the
	// collection dropdown was the only row visible.
	return container.NewBorder(header, nil, nil, nil,
		container.NewBorder(
			container.NewVBox(
				container.NewVScroll(container.NewVBox(
					form,
					container.NewHBox(save, test),
					widget.NewSeparator(),
					widget.NewLabelWithStyle("Connection (tie config)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				)),
			),
			nil, nil, nil,
			connEditor,
		))
}
