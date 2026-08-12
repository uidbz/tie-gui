package gallery

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"git.sr.ht/~uid/imgview/mpvplayer"
)

// OpenVideoWindow creates and shows a video player window with standard layout:
// 800x520 size, close intercept that stops the player, and optional cleanup.
// The displayPath is used to construct the window title ("Video: basename").
// The onClose callback, if non-nil, is called after the player is closed
// (useful for cleaning up temp files in tieview). This factors the video
// window creation pattern duplicated in both mains.
//
// Must be called on the UI goroutine (via fyne.Do).
func OpenVideoWindow(app fyne.App, player *mpvplayer.MPVPlayer, displayPath string, onClose func()) {
	title := "Video: " + filepath.Base(displayPath)
	w := app.NewWindow(title)
	v := mpvplayer.NewVideo(player)
	w.SetCloseIntercept(func() {
		v.Close()
		w.Close()
		if onClose != nil {
			onClose()
		}
	})
	w.SetContent(v)
	w.Resize(fyne.NewSize(800, 520))
	w.Show()
}
