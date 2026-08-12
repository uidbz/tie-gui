package gallery

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

// NewApp creates a Fyne application with the given ID, icon, and window title.
// This factors the common bootstrap pattern shared by imgview and tieview.
func NewApp(appID, windowTitle string, iconData []byte) (fyne.App, fyne.Window) {
	a := app.NewWithID(appID)
	a.SetIcon(fyne.NewStaticResource("icon", iconData))
	w := a.NewWindow(windowTitle)
	return a, w
}
