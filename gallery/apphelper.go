package gallery

import (
	"flag"
	"strings"

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

// ConfigFlag defines -config/-c flags with the given help text and returns a
// pointer to receive the value. The caller must call flag.Parse() after all
// flags are defined. Use NormalizeConfigPath to append .toml suffix if needed.
// This factors the duplicated flag definition in both mains.
func ConfigFlag(helpText string) *string {
	configPath := flag.String("config", "", helpText)
	flag.StringVar(configPath, "c", "", "Shorthand for -config")
	return configPath
}

// NormalizeConfigPath appends .toml extension to path if not present and non-empty.
// This factors the duplicated suffix logic in both mains.
func NormalizeConfigPath(path string) string {
	if path != "" && !strings.HasSuffix(path, ".toml") {
		return path + ".toml"
	}
	return path
}
