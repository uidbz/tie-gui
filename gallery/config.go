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
	Quit            []fyne.KeyName
	ScrollDown      []fyne.KeyName
	ScrollUp        []fyne.KeyName
	PathLevelUp     []fyne.KeyName
	ToggleFilenames []fyne.KeyName
}

// LoadConfig returns the bundled default config overlaid with an explicit
// config file when configPath is non-empty, or with the user's
// ~/.config/imgview/config.toml otherwise.
func LoadConfig(window fyne.Window, configPath string) (config Config) {
	if err := toml.Unmarshal(configData, &config); err != nil {
		panic("Bundled config is not valid TOML: " + err.Error())
	}
	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
	}

	var overlayPath string
	if configPath != "" {
		overlayPath = configPath
	} else if dir, err := os.UserConfigDir(); err == nil {
		candidate := filepath.Join(dir, "imgview", "config.toml")
		if _, err2 := os.Stat(candidate); !os.IsNotExist(err2) {
			overlayPath = candidate
		}
	}

	if overlayPath != "" {
		configFile, errRead := os.ReadFile(overlayPath)
		if errRead != nil {
			dialog.ShowError(errors.New("Error reading config file: "+errRead.Error()), window)
			window.ShowAndRun()
		} else if err3 := toml.Unmarshal(configFile, &config); err3 != nil {
			dialog.ShowError(errors.New("Config file error: "+err3.Error()+"\nin: "+overlayPath), window)
			window.ShowAndRun()
		}
	}

	if config.General.ThumbnailDir == "" {
		config.General.ThumbnailDir = filepath.Join(os.TempDir(), "imgview")
	}

	return config
}

// AdjustForMobile modifies config parameters to be more memory-efficient on
// mobile devices. Call this after LoadConfig if running on mobile.
func (c *Config) AdjustForMobile() {
	// Smaller tiles to fit mobile screens and reduce memory
	if c.General.TileWidth > 200 {
		c.General.TileWidth = 200
	}
	// Fewer items per page to reduce memory usage
	if c.General.ImagesPerPage > 100 {
		c.General.ImagesPerPage = 100
	}
	// Fewer workers to reduce CPU/memory overhead
	if c.General.Workers > 4 {
		c.General.Workers = 4
	}
	// Slightly smaller gap for compact display
	if c.General.TileGap > 3 {
		c.General.TileGap = 3
	}
}
