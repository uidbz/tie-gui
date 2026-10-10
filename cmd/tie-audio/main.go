package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	forceExitOnSignal(3 * time.Second)

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

// forceExitOnSignal makes SIGINT/SIGTERM (Ctrl+C, kill) always end the
// process. Fyne handles those signals by queueing a Quit onto the UI
// goroutine, so if that goroutine is ever blocked the signal does nothing and
// only SIGKILL helps (tie-audio once hung like this in the playback engine's
// Close). This handler runs alongside Fyne's: it gives the normal shutdown
// grace to finish, then exits; a second signal exits immediately.
func forceExitOnSignal(grace time.Duration) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		select {
		case <-ch:
		case <-time.After(grace):
			fmt.Fprintln(os.Stderr, "tie-audio: shutdown did not finish, forcing exit")
		}
		os.Exit(130)
	}()
}
