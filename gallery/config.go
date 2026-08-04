package gallery

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/pelletier/go-toml/v2"
)

//go:embed config.toml
var configData []byte

type Config struct {
	Image   ImageConfig
	Gallery GalleryConfig
	General GeneralConfig
}

type GeneralConfig struct {
	ThumbnailDir  string
	DefaultWidth  float32
	DefaultHeight float32
	TileWidth     float32
	TileGap       float32
	Workers       int
	ImagesPerPage int
}

type ImageConfig struct {
	NextImage     []fyne.KeyName
	PreviousImage []fyne.KeyName
	RotateLeft    []fyne.KeyName
	RotateRight   []fyne.KeyName
	OriginalSize  []fyne.KeyName
	FillWindow    []fyne.KeyName
	Filtering     []fyne.KeyName
	ShowGallery   []fyne.KeyName
	Quit          []fyne.KeyName
	FullScreen    []fyne.KeyName
	RunCmda       []fyne.KeyName
	ShowTagbar    []fyne.KeyName
	CmdA          string
	SaveImage     []fyne.KeyName
	SaveDir       string
}

type GalleryConfig struct {
	Quit        []fyne.KeyName
	ScrollDown  []fyne.KeyName
	ScrollUp    []fyne.KeyName
	PathLevelUp []fyne.KeyName
}

// LoadConfig returns the bundled default config overlaid with the user's
// ~/.config/imgview/config.toml when present.
func LoadConfig(window fyne.Window) (config Config) {
	if err := toml.Unmarshal(configData, &config); err != nil {
		panic("Bundled config is not valid TOML: " + err.Error())
	}
	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		imgviewConfig := filepath.Join(dir, "imgview", "config.toml")
		if _, err2 := os.Stat(imgviewConfig); !os.IsNotExist(err2) {
			configFile, errRead := os.ReadFile(imgviewConfig)
			if errRead != nil {
				dialog.ShowError(errors.New("Error reading config file: "+errRead.Error()), window)
				window.ShowAndRun()
			}
			if err3 := toml.Unmarshal(configFile, &config); err3 != nil {
				dialog.ShowError(errors.New("Config file error: "+err3.Error()+"\nin: "+imgviewConfig), window)
				window.ShowAndRun()
			}
		}
	}
	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
	}

	return config
}
