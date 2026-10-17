package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/mpris"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/ui"
	"github.com/uidbz/tie-gui/internal/appboot"
	"github.com/uidbz/tie-gui/tieconfig"
)

func main() {
	// -control drives an already running instance over MPRIS and exits,
	// without opening a window — for window-manager key bindings, e.g. sway:
	//   bindsym XF86AudioPlay exec tie-audio -control play-pause
	control := flag.String("control", "",
		"send an action to the running tie-audio and exit: "+strings.Join(mpris.Actions, ", "))
	var boot appboot.Options
	appboot.AddFlags(flag.CommandLine, &boot)
	flag.Parse()
	if *control != "" {
		if err := mpris.Control(*control); err != nil {
			fmt.Fprintln(os.Stderr, "tie-audio:", err)
			os.Exit(1)
		}
		return
	}

	// The -control path above is a one-shot CLI call: it reports on stderr and
	// exits, and deliberately does not open a log file.
	cfg, _ := config.Load()

	// Logging, -conf-show, Ctrl-C handling and the sweep of staging files an
	// earlier killed run left in /tmp (see internal/appboot).
	boot.App = "tie-audio"
	boot.ConfigName = cfg.TieConfig
	boot.CleanTemp = true
	boot.Grace = 3 * time.Second
	boot.Collection = func(tieCfg client.Config) string {
		return tieconfig.AppCollection(tieCfg, cfg.TieCollection, config.DefaultTieCollection)
	}
	run := appboot.Start(boot)
	defer run.Close()
	run.MaybeShowConfig()
	appLog := run.Log
	ui.SetLogger(appLog)
	data.SetLogger(appLog)

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
