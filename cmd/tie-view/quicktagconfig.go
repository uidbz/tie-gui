package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"fyne.io/fyne/v2"

	"github.com/pelletier/go-toml/v2"
)

// Heart icons for the built-in "favorite" quick tag: red = applied, grey = not.
//
//go:embed heart.png
var heartPNG []byte

//go:embed heart-grey.png
var heartGreyPNG []byte

var (
	heartRes     = fyne.NewStaticResource("heart.png", heartPNG)
	heartGreyRes = fyne.NewStaticResource("heart-grey.png", heartGreyPNG)
)

// builtinQuickTagIcons are icon names that resolve without a file on disk.
// The star icons are shared with the rating widget (rating.go).
var builtinQuickTagIcons = map[string]fyne.Resource{
	"heart.png":       heartRes,
	"heart-grey.png":  heartGreyRes,
	"star-filled.png": starFilledRes,
	"star-empty.png":  starEmptyRes,
}

// QuickTagSet describes one quick tagging bar: which tags it offers, in which
// order, with which icons and shortcut keys, and where it sits. Exported
// (unlike the rest of this package) because it is embedded in quickTagConfig
// to flatten its fields to the TOML top level, and go-toml's marshaler skips
// embedded fields whose type name is unexported.
type QuickTagSet struct {
	// Position places the bar at the "bottom" (default) or "top" of the image.
	Position string `toml:"Position,omitempty"`
	// IconSize is the button edge length in points; 0 selects the platform
	// default (see quickTagIconSize).
	IconSize float32 `toml:"IconSize,omitempty"`
	// Tags are the bar's buttons, left to right.
	Tags []quickTagEntry `toml:"Tag"`
}

// quickTagConfig is the on-disk quicktags.toml: a default set at the top
// level plus optional per-collection overrides keyed by the tie config's
// collection name ([Collections.<name>] tables).
type quickTagConfig struct {
	QuickTagSet
	// Collections maps a tie collection name to its override. A collection
	// with an entry here uses the entry's Tag list instead of the default one
	// (even an empty list); its Position and IconSize fall back to the
	// top-level values when unset.
	Collections map[string]QuickTagSet `toml:"Collections,omitempty"`
}

// For resolves the set to show for a collection: its override merged over
// the default as described on Collections, or the default when it has none.
func (cfg quickTagConfig) For(collection string) QuickTagSet {
	set := cfg.QuickTagSet
	ov, ok := cfg.Collections[collection]
	if !ok || collection == "" {
		return set
	}
	set.Tags = ov.Tags
	if ov.Position != "" {
		set.Position = ov.Position
	}
	if ov.IconSize > 0 {
		set.IconSize = ov.IconSize
	}
	return set
}

// HasOverride reports whether collection has its own [Collections.<name>]
// table.
func (cfg quickTagConfig) HasOverride(collection string) bool {
	_, ok := cfg.Collections[collection]
	return ok && collection != ""
}

// SetOverride stores set as collection's override (creating the map).
func (cfg *quickTagConfig) SetOverride(collection string, set QuickTagSet) {
	if cfg.Collections == nil {
		cfg.Collections = map[string]QuickTagSet{}
	}
	cfg.Collections[collection] = set
}

// RemoveOverride drops collection's override so it uses the default set.
func (cfg *quickTagConfig) RemoveOverride(collection string) {
	delete(cfg.Collections, collection)
	if len(cfg.Collections) == 0 {
		cfg.Collections = nil
	}
}

// quickTagEntry is one button on the quick tagging bar.
type quickTagEntry struct {
	// Tag is the tie tag the button toggles.
	Tag string `toml:"Tag"`
	// On is the icon shown while the tag is applied to the image. Relative
	// paths resolve against the config file's directory; the built-in names in
	// builtinQuickTagIcons need no file. Empty (with Off empty too) renders
	// the tag name as a text button instead.
	On string `toml:"On,omitempty"`
	// Off is the icon shown while the tag is not applied. Empty shows a dimmed
	// copy of On.
	Off string `toml:"Off,omitempty"`
	// Key is the keyboard shortcut (a Fyne key name such as "1" or "F").
	// Empty defaults to the button's 1-based position for the first nine
	// buttons.
	Key string `toml:"Key,omitempty"`
}

// defaultQuickTagTOML is written to quickTagConfigPath on first run so users
// find a commented, editable file rather than having to discover the format.
const defaultQuickTagTOML = `# tie-view quick tagging bar.
#
# Each [[Tag]] entry is one button on the bar, left to right. Icons are square
# PNGs: "On" is shown while the tag is applied to the image, "Off" while it is
# not. Leave Off empty to show a dimmed copy of On; leave both empty for a text
# button. Paths are relative to this file's directory unless absolute. The
# built-in icons heart.png, heart-grey.png, star-filled.png and star-empty.png
# need no file.
#
# Key is the keyboard shortcut (a Fyne key name, e.g. "1" or "F"). Unset keys
# default to the button's position: 1, 2, ... 9. Position is "bottom" or "top".
# The bar itself is toggled with the [Image] ShowTagbar key (T) or the menu.
#
# The top-level entries are the default bar. A tie collection can get its own
# bar with a [Collections.<name>] table (name as in the tie config), whose
# [[Collections.<name>.Tag]] entries replace the default list; Position and
# IconSize in that table are optional overrides:
#
#   [Collections.photos]
#   Position = "top"
#   [[Collections.photos.Tag]]
#   Tag = "print"
#   On = "icons/printer.png"

Position = "bottom"

[[Tag]]
Tag = "favorite"
On = "heart.png"
Off = "heart-grey.png"
`

// defaultQuickTagConfig is the in-memory equivalent of defaultQuickTagTOML.
func defaultQuickTagConfig() quickTagConfig {
	var cfg quickTagConfig
	if err := toml.Unmarshal([]byte(defaultQuickTagTOML), &cfg); err != nil {
		panic("default quick tag config is not valid TOML: " + err.Error())
	}
	return cfg
}

// quickTagConfigDir is the per-app config directory holding quicktags.toml
// and any user icons referenced by relative path. On Android os.UserConfigDir
// cannot resolve (no $HOME), so use $FILESDIR like tieconfig.Dir.
func quickTagConfigDir() string {
	base := os.Getenv("FILESDIR")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "tieview")
}

// quickTagConfigPath is the quick tag config file location.
func quickTagConfigPath() string {
	return filepath.Join(quickTagConfigDir(), "quicktags.toml")
}

// loadQuickTagConfig reads the config at path. A missing file yields the
// default config and writes defaultQuickTagTOML there (best effort) so the
// user has a template to edit; a malformed file is reported and falls back
// to the default without overwriting the user's file.
func loadQuickTagConfig(path string) quickTagConfig {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if werr := writeFileMkdir(path, []byte(defaultQuickTagTOML)); werr != nil {
			fmt.Println("quicktag: cannot write default config:", werr)
		}
		return defaultQuickTagConfig()
	}
	if err != nil {
		fmt.Println("quicktag: cannot read config:", err)
		return defaultQuickTagConfig()
	}
	var cfg quickTagConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("quicktag: config error in %s: %v\n", path, err)
		return defaultQuickTagConfig()
	}
	return cfg
}

// saveQuickTagConfig writes cfg to path as TOML, creating the directory.
func saveQuickTagConfig(path string, cfg quickTagConfig) error {
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	return writeFileMkdir(path, data)
}

// writeFileMkdir writes data to path, creating parent directories as needed.
func writeFileMkdir(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// normalized returns a copy of set with blank-tag entries dropped, Position
// lowercased with "bottom" as the fallback, and position-based default keys
// filled in for entries without one (buttons 1-9). Keys are not deduplicated:
// a user who binds two buttons to one key toggles both, which is a feature.
func (set QuickTagSet) normalized() QuickTagSet {
	out := set
	out.Tags = nil
	for _, e := range set.Tags {
		if e.Tag == "" {
			continue
		}
		if e.Key == "" && len(out.Tags) < 9 {
			e.Key = strconv.Itoa(len(out.Tags) + 1)
		}
		out.Tags = append(out.Tags, e)
	}
	if out.Position != "top" {
		out.Position = "bottom"
	}
	return out
}

// resolveQuickTagIcon loads the icon named by name: an absolute path, a path
// relative to baseDir, or one of the built-in names. It returns nil (and
// logs) when name is non-empty but nothing matches, so the cell falls back
// to a text button rather than failing.
func resolveQuickTagIcon(baseDir, name string) fyne.Resource {
	if name == "" {
		return nil
	}
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	if data, err := os.ReadFile(p); err == nil {
		return fyne.NewStaticResource(p, data)
	}
	if r, ok := builtinQuickTagIcons[name]; ok {
		return r
	}
	fmt.Printf("quicktag: icon %q not found (looked in %s)\n", name, baseDir)
	return nil
}
