package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/mpris"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/ui"
)

func main() {
	// -control drives an already running instance over MPRIS and exits,
	// without opening a window — for window-manager key bindings, e.g. sway:
	//   bindsym XF86AudioPlay exec tie-audio -control play-pause
	control := flag.String("control", "",
		"send an action to the running tie-audio and exit: "+strings.Join(mpris.Actions, ", "))
	flag.Parse()
	if *control != "" {
		if err := mpris.Control(*control); err != nil {
			fmt.Fprintln(os.Stderr, "tie-audio:", err)
			os.Exit(1)
		}
		return
	}

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
