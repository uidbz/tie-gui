package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/ui"
)

func main() {
	cfg, _ := config.Load()
	session := data.NewSession(cfg)

	a := app.New()
	w := a.NewWindow("tie-audio")
	root := ui.NewApp(w, session)
	w.SetContent(root.Root())
	w.Resize(fyne.NewSize(1000, 700))
	if session.BackendErr != nil {
		dialog.ShowError(fmt.Errorf("local playback unavailable, using pwplay remote: %w", session.BackendErr), w)
	}
	w.ShowAndRun()
}
