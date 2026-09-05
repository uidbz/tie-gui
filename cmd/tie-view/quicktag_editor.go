package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// quickTagDefaultLabel is the dropdown label for the top-level default set.
const quickTagDefaultLabel = "Default (all collections)"

// makeQuickTagEditor builds the Settings → Quick tags page. A "Bar for"
// dropdown picks which set is edited: the default or one tie collection's
// override. Below it, one card per bar button (tag name, shortcut key, On/Off
// icon paths with a file picker and a live preview, reorder and delete), plus
// position and icon size. Apply writes quicktags.toml to path and hands the
// whole config to onApply so the running bar rebuilds; "Use default bar"
// drops the selected collection's override; Reload re-reads the file for
// users who edit it by hand. Picked icon files are copied into <config
// dir>/icons so the config stays portable and works on Android, where the
// picked URI is not a stable path.
//
// collections returns the tie config's collection names and the active one;
// the returned refresh func re-queries it (call after a connection change) and
// switches the editor to the newly active collection.
func makeQuickTagEditor(window fyne.Window, path string, cfg quickTagConfig, collections func() (names []string, active string), onApply func(quickTagConfig)) (fyne.CanvasObject, func()) {
	baseDir := filepath.Dir(path)

	// Form model for the set being edited.
	var entries []quickTagEntry
	position := "bottom"
	editing := "" // "" = default set, else a collection name

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	scopeNote := widget.NewLabel("")
	scopeNote.Wrapping = fyne.TextWrapWord
	rows := container.NewVBox()

	positionSelect := widget.NewSelect([]string{"bottom", "top"}, func(s string) { position = s })
	iconSize := widget.NewEntry()
	iconSize.SetPlaceHolder("auto")

	var rebuild func()

	// iconField is an icon path entry with a browse button and a preview
	// that tracks the entry text. set stores the path into the model.
	iconField := func(label, value string, set func(string)) fyne.CanvasObject {
		preview := canvas.NewImageFromResource(nil)
		preview.FillMode = canvas.ImageFillContain
		preview.SetMinSize(fyne.NewSize(28, 28))
		entry := widget.NewEntry()
		entry.SetPlaceHolder("icon.png")
		refreshPreview := func(name string) {
			preview.Resource = resolveQuickTagIcon(baseDir, name)
			preview.Refresh()
		}
		entry.OnChanged = func(s string) {
			set(s)
			refreshPreview(s)
		}
		entry.SetText(value) // fires OnChanged → preview
		browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
			fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
				if err != nil {
					status.SetText("Pick failed: " + err.Error())
					return
				}
				if rc == nil {
					return
				}
				defer rc.Close()
				rel, err := importQuickTagIcon(baseDir, rc)
				if err != nil {
					status.SetText("Import failed: " + err.Error())
					return
				}
				entry.SetText(rel)
			}, window)
			fd.SetFilter(storage.NewExtensionFileFilter([]string{".png", ".PNG"}))
			fd.Show()
		})
		return container.NewBorder(nil, nil,
			container.NewHBox(preview, widget.NewLabel(label)), browse, entry)
	}

	rebuild = func() {
		rows.Objects = nil
		for i := range entries {
			i := i
			e := &entries[i]

			tag := widget.NewEntry()
			tag.SetPlaceHolder("tag name")
			tag.SetText(e.Tag)
			tag.OnChanged = func(s string) { e.Tag = strings.TrimSpace(s) }

			key := widget.NewEntry()
			key.SetPlaceHolder(strconv.Itoa(i + 1))
			key.SetText(e.Key)
			key.OnChanged = func(s string) { e.Key = strings.TrimSpace(s) }
			keyBox := container.New(layout.NewGridWrapLayout(fyne.NewSize(52, key.MinSize().Height)), key)

			up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				if i > 0 {
					entries[i-1], entries[i] = entries[i], entries[i-1]
					rebuild()
				}
			})
			down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				if i < len(entries)-1 {
					entries[i+1], entries[i] = entries[i], entries[i+1]
					rebuild()
				}
			})
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				entries = append(entries[:i], entries[i+1:]...)
				rebuild()
			})
			del.Importance = widget.DangerImportance
			head := container.NewBorder(nil, nil, nil, container.NewHBox(keyBox, up, down, del), tag)

			card := container.NewVBox(
				head,
				iconField("On", e.On, func(s string) { e.On = strings.TrimSpace(s) }),
				iconField("Off", e.Off, func(s string) { e.Off = strings.TrimSpace(s) }),
				widget.NewSeparator(),
			)
			rows.Objects = append(rows.Objects, card)
		}
		rows.Refresh()
	}

	// collect reads the form back into a set.
	collect := func() (QuickTagSet, error) {
		out := QuickTagSet{Position: position, Tags: append([]quickTagEntry(nil), entries...)}
		if s := strings.TrimSpace(iconSize.Text); s != "" {
			v, err := strconv.ParseFloat(s, 32)
			if err != nil || v <= 0 {
				return out, fmt.Errorf("icon size must be a positive number")
			}
			out.IconSize = float32(v)
		}
		return out, nil
	}

	var useDefault *widget.Button

	// loadForm shows the set the selected scope resolves to. A collection
	// without an override shows the default set as a starting point.
	loadForm := func() {
		set := cfg.For(editing)
		entries = append([]quickTagEntry(nil), set.Tags...)
		position = set.Position
		if position == "" {
			position = "bottom"
		}
		positionSelect.SetSelected(position)
		if set.IconSize > 0 {
			iconSize.SetText(strconv.FormatFloat(float64(set.IconSize), 'f', -1, 32))
		} else {
			iconSize.SetText("")
		}
		rebuild()
		switch {
		case editing == "":
			scopeNote.SetText("The default bar, used by every collection without its own.")
			useDefault.Hide()
		case cfg.HasOverride(editing):
			scopeNote.SetText(fmt.Sprintf("Collection %q has its own bar.", editing))
			useDefault.Show()
		default:
			scopeNote.SetText(fmt.Sprintf("Collection %q uses the default bar. Edit and Apply to give it its own.", editing))
			useDefault.Hide()
		}
	}

	// storeForm writes the form back into cfg (in memory) so switching scope
	// keeps unsaved edits. A collection that had no override only gains one
	// when the form actually differs from the default, so merely looking at
	// a collection does not create an override on the next Apply.
	storeForm := func() error {
		set, err := collect()
		if err != nil {
			return err
		}
		if editing == "" {
			cfg.QuickTagSet = set
		} else if cfg.HasOverride(editing) || !quickTagSetsEqual(set, cfg.QuickTagSet) {
			cfg.SetOverride(editing, set)
		}
		return nil
	}

	// Scope dropdown: default plus every configured collection.
	var scopeNames []string // parallel to the Select's options; "" = default
	scope := widget.NewSelect(nil, nil)
	refreshScope := func(selectName string) {
		names, _ := collections()
		names = slices.Clone(names)
		// Collections that only exist as overrides in the file (e.g. from a
		// removed tie config entry) stay editable so they can be cleaned up.
		for name := range cfg.Collections {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		scopeNames = append([]string{""}, names...)
		opts := []string{quickTagDefaultLabel}
		for _, n := range names {
			opts = append(opts, n)
		}
		scope.Options = opts
		idx := slices.Index(scopeNames, selectName)
		if idx < 0 {
			idx = 0
		}
		editing = scopeNames[idx]
		scope.Selected = opts[idx]
		scope.Refresh()
		loadForm()
	}
	scope.OnChanged = func(label string) {
		idx := slices.Index(scope.Options, label)
		if idx < 0 || scopeNames[idx] == editing {
			return
		}
		if err := storeForm(); err != nil {
			status.SetText(err.Error())
		}
		editing = scopeNames[idx]
		loadForm()
	}

	add := widget.NewButtonWithIcon("Add tag", theme.ContentAddIcon(), func() {
		entries = append(entries, quickTagEntry{})
		rebuild()
	})

	save := func(msg string) {
		if err := saveQuickTagConfig(path, cfg); err != nil {
			status.SetText("Save failed: " + err.Error())
			return
		}
		if onApply != nil {
			onApply(cfg)
		}
		status.SetText(msg)
	}

	apply := widget.NewButton("Apply", func() {
		set, err := collect()
		if err != nil {
			status.SetText(err.Error())
			return
		}
		if editing == "" {
			cfg.QuickTagSet = set
		} else {
			cfg.SetOverride(editing, set)
		}
		loadForm() // refresh the scope note / Use default button
		save("Saved to " + path)
	})
	apply.Importance = widget.HighImportance

	useDefault = widget.NewButton("Use default bar", func() {
		if editing == "" {
			return
		}
		cfg.RemoveOverride(editing)
		loadForm()
		save(fmt.Sprintf("Collection %q now uses the default bar.", editing))
	})

	reload := widget.NewButton("Reload file", func() {
		cfg = loadQuickTagConfig(path)
		refreshScope(editing)
		if onApply != nil {
			onApply(cfg)
		}
		status.SetText("Reloaded " + path)
	})

	help := widget.NewLabel("Buttons appear left to right. Icons are square PNGs; leave Off empty to dim the On icon, or both empty for a text button. Built-in: heart.png, heart-grey.png, star-filled.png, star-empty.png. Keys default to 1-9. Toggle the bar with T or the ☰ menu.\n\nFile: " + path)
	help.Wrapping = fyne.TextWrapWord

	general := container.NewGridWithColumns(2,
		container.NewBorder(nil, nil, widget.NewLabel("Position"), nil, positionSelect),
		container.NewBorder(nil, nil, widget.NewLabel("Icon size"), nil, iconSize),
	)

	_, active := collections()
	refreshScope(active)

	page := container.NewVScroll(container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Bar for"), nil, scope),
		scopeNote,
		useDefault,
		widget.NewSeparator(),
		general,
		widget.NewSeparator(),
		rows,
		add,
		container.NewGridWithColumns(2, apply, reload),
		status,
		help,
	))

	// refresh follows a connection change: new collection list, and the
	// editor jumps to the now-active collection so the bar being edited is
	// the one on screen.
	refresh := func() {
		if err := storeForm(); err != nil {
			status.SetText(err.Error())
		}
		_, active := collections()
		refreshScope(active)
	}
	return page, refresh
}

// quickTagSetsEqual compares two sets field by field (nil and empty Tags
// are equal).
func quickTagSetsEqual(a, b QuickTagSet) bool {
	if a.Position != b.Position || a.IconSize != b.IconSize || len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}

// importQuickTagIcon copies a picked icon into <baseDir>/icons and returns its
// config-relative path. An existing file with identical content is reused; a
// name clash with different content gets a numeric suffix.
func importQuickTagIcon(baseDir string, rc fyne.URIReadCloser) (string, error) {
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	base := filepath.Base(rc.URI().Name())
	if base == "" || base == "." || base == "/" {
		base = "icon.png"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	iconDir := filepath.Join(baseDir, "icons")
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return "", err
	}
	name := base
	for i := 2; ; i++ {
		dst := filepath.Join(iconDir, name)
		existing, rerr := os.ReadFile(dst)
		if os.IsNotExist(rerr) {
			if werr := os.WriteFile(dst, data, 0o644); werr != nil {
				return "", werr
			}
			break
		}
		if rerr == nil && bytes.Equal(existing, data) {
			break
		}
		name = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	return filepath.ToSlash(filepath.Join("icons", name)), nil
}
