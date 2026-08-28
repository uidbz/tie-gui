package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio-player/internal/ui"
)

func main() {
	cfg, _ := config.Load()
	session := data.NewSession(cfg)

	a := app.New()
	w := a.NewWindow("tie-audio-player")
	root := ui.NewApp(w, session)
	w.SetContent(root.Root())
	w.Resize(fyne.NewSize(1000, 700))
	w.ShowAndRun()
}
