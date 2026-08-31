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
			// Preserve column customization, which is edited from the album/queue
			// views, not this form; rebuilding the config from scratch would drop it.
			AlbumColumns: a.session.Cfg.AlbumColumns,
			QueueColumns: a.session.Cfg.QueueColumns,
		}
	}

	form := widget.NewForm(
		widget.NewFormItem("pwplay server", server),
		widget.NewFormItem("tie config", tieCfg),
		widget.NewFormItem("filehost", fileHost),
	)

	// rebuildSession reloads the tie + pwplay clients from the persisted app
	// config so a connection change takes effect immediately.
	rebuildSession := func() {
		a.session = data.NewSession(a.session.Cfg)
		a.browse.session = a.session
		a.browse.loadTags()
	}

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

	// Connection editor: edit the tie config (triplestore/collection/filehosts) as
	// TOML in-app, so the connection can be set up comfortably on Android
	// (matching tie-view). Save the file the current tie config resolves to and
	// rebuild the session on Apply.
	connEditor := tieconfig.Editor(a.session.Tie.Config, tieconfig.ResolvePath(a.session.Cfg.TieConfig),
		func(_ tieclient.Config) { rebuildSession() })

	return container.NewBorder(header, nil, nil, nil,
		container.NewVScroll(container.NewVBox(
			form,
			container.NewHBox(save, test),
			widget.NewSeparator(),
			widget.NewLabelWithStyle("Connection (tie config)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			connEditor,
		)))
}
