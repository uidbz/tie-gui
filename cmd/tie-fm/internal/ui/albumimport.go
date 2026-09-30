package ui

import (
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/tagselection"
)

// importAsAlbums starts a bulk album import of one or more local directories
// into tie: each directory is scanned and clustered into albums
// (client.PlanAlbumImport), the user reviews the combined plan (destinations,
// track counts, warnings), and the confirmed groups import through the ops
// engine, each landing at its rendered destination verbatim
// (Operations.ImportAlbum).
func (fm *FileManager) importAsAlbums(entries []fs.Entry) {
	fm.markActive()
	entries = albumImportRoots(entries)
	if len(entries) == 0 {
		return
	}
	title := "Import as albums: " + entries[0].Name
	if len(entries) > 1 {
		title = fmt.Sprintf("Import as albums: %d directories", len(entries))
	}
	backend := fm.registry.For("tie:/")
	if _, ok := backend.(fs.Importer); !ok {
		return
	}
	provider, ok := backend.(interface{ Client() *client.TieClient })
	if !ok {
		dialog.ShowError(errors.New("album import requires the tie backend"), fm.win)
		return
	}
	tc := provider.Client()

	// The dir-type picks the destination template (the tie config's
	// ImportDest) and the label stamped on each imported album root — the same
	// choice the "Copy into tie as" submenu offers, defaulting to audio-dir.
	// Tags are applied to every imported album and its tracks, so the albums
	// are queryable in media apps (tie-audio's tag-driven cover wall).
	typeSel := widget.NewSelect(fs.BuiltinDirTypes, nil)
	typeSel.SetSelected("audio-dir")
	custom := widget.NewEntry()
	custom.PlaceHolder = "custom type (optional)"
	tagSel := fm.newAlbumTagSelector(backend)

	// The destination template renders each album's virtual path from its
	// aggregated tags; the plan review dialog shows the rendered result per
	// album before anything imports. It pre-fills from the tie config's
	// ImportDest entry for the picked type, falling back to the common music
	// layout; an empty field keeps the legacy source-path placement.
	tmplEntry := widget.NewEntry()
	tmplEntry.PlaceHolder = "/{albumartist}/{year} - {album}  (empty: keep source paths)"
	remember := widget.NewCheck("Remember as default for this type", nil)

	resolvedType := func() string {
		if t := strings.TrimSpace(custom.Text); t != "" {
			return t
		}
		return typeSel.Selected
	}
	var edited, refilling bool
	tmplEntry.OnChanged = func(string) {
		if !refilling {
			edited = true
		}
	}
	fillTemplate := func() {
		if edited {
			return // keep the user's edit across type changes
		}
		tmpl := tc.Config.ImportDest[resolvedType()]
		if tmpl == "" {
			tmpl = defaultAlbumTemplate
		}
		refilling = true
		tmplEntry.SetText(tmpl)
		refilling = false
	}
	typeSel.OnChanged = func(string) { fillTemplate() }
	custom.OnChanged = func(string) { fillTemplate() }
	fillTemplate()

	var form *dialog.FormDialog
	form = dialog.NewForm(title, "Scan", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Directory type", typeSel),
			widget.NewFormItem("Custom", custom),
			widget.NewFormItem("Destination", tmplEntry),
			widget.NewFormItem("", remember),
			widget.NewFormItem("Tags", tagSel),
		}, func(ok bool) {
			if !ok {
				return
			}
			dirType := resolvedType()
			if dirType == "" {
				return
			}
			tmpl := strings.TrimSpace(tmplEntry.Text)
			if err := client.ValidateDestTemplate(tmpl); err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
			if remember.Checked {
				fm.saveAlbumTemplate(tc, dirType, tmpl)
			}
			tags, _ := tagSel.SelectedTags()
			fm.scanAlbumPlan(entries, tc.Config, dirType, tmpl, tags)
		}, fm.win)
	// The dialog sizes itself once from its MinSize; grow/shrink it as the
	// tag search dropdown opens and closes and the chip row appears.
	tagSel.OnMinSizeChanged = func() { form.Resize(fyne.NewSize(0, 0)) }
	form.Show()
}

// newAlbumTagSelector builds the album import form's tag picker: a search
// entry over every known tag (loaded off the UI goroutine from the tie
// backend) with the picked tags shown as removable chips in a row below it.
// Enter on text matching no highlighted result creates a new tag.
func (fm *FileManager) newAlbumTagSelector(backend fs.FileSystem) *tagselection.TagSelection {
	ts := tagselection.NewTagChipSelection(fm.win)
	ts.OnNewTag = func(tag string) { ts.AddSelected(tagselection.NewTagItemData(tag)) }
	if store, ok := backend.(fs.TagStore); ok {
		go func() {
			tags, err := store.ListAllTags()
			if err != nil {
				return // free-form tags still work
			}
			fyne.Do(func() {
				for _, t := range tags {
					ts.AddTag(t)
				}
			})
		}()
	}
	return ts
}

// defaultAlbumTemplate is the destination template suggested when the tie
// config's ImportDest has no entry for the picked directory type: the common
// "Artist/Year - Album" music layout ({albumartist} falls back to artist).
const defaultAlbumTemplate = "/{albumartist}/{year} - {album}"

// saveAlbumTemplate records tmpl as the tie config's ImportDest entry for
// dirType — the default the tie CLI and future imports render from — and
// persists the config file. A save failure is reported but not fatal: the
// import proceeds with the template either way.
func (fm *FileManager) saveAlbumTemplate(tc *client.TieClient, dirType, tmpl string) {
	if tc.Config.ImportDest == nil {
		tc.Config.ImportDest = map[string]string{}
	}
	if tc.Config.ImportDest[dirType] == tmpl {
		return
	}
	tc.Config.ImportDest[dirType] = tmpl
	name := tc.Config.Path()
	if name == "" {
		// tie-fm runs on its embedded default config: materialize it as the
		// standard user config so the CLI picks the template up too.
		name = "config.toml"
	}
	if err := client.SaveConfig(name, tc.Config); err != nil {
		dialog.ShowError(fmt.Errorf("template default not saved: %w", err), fm.win)
	}
}

// albumImportRoots filters entries down to the local directories an album
// import can scan, dropping duplicates and directories nested inside another
// selected one (their albums are already found by the outer scan, and would
// otherwise be planned — and imported — twice).
func albumImportRoots(entries []fs.Entry) []fs.Entry {
	root := func(e fs.Entry) string {
		return filepath.Clean(strings.TrimPrefix(e.Path, "file:"))
	}
	var dirs []fs.Entry
	for _, e := range entries {
		if e.IsDir && fs.IsLocal(e.Path) {
			dirs = append(dirs, e)
		}
	}
	var out []fs.Entry
	for i, e := range dirs {
		p := root(e)
		covered := false
		for j, o := range dirs {
			if i == j {
				continue
			}
			op := root(o)
			if (op == p && j < i) || strings.HasPrefix(p, op+string(filepath.Separator)) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, e)
		}
	}
	return out
}

// scanAlbumPlan runs the album planner off the UI goroutine — probing a large
// or network-mounted library can take minutes, so progress is shown. Each
// selected directory is scanned in turn and the groups are concatenated into
// one plan; a directory that fails to scan is reported without discarding
// the others' albums. The finished plan (or the scan errors) returns to the
// UI goroutine via fyne.Do. template overrides the tie config's ImportDest
// entry for dirType (an empty string falls back to the config, then to
// source-path placement).
func (fm *FileManager) scanAlbumPlan(entries []fs.Entry, cfg client.Config, dirType, template string, tags []string) {
	roots := make([]string, len(entries))
	for i, e := range entries {
		roots[i] = strings.TrimPrefix(e.Path, "file:")
	}
	status := widget.NewLabel("Scanning " + roots[0] + " …")
	bar := widget.NewProgressBarInfinite()
	scan := dialog.NewCustomWithoutButtons("Import as albums",
		container.NewVBox(status, bar), fm.win)
	scan.Show()
	go func() {
		var plan []client.AlbumGroup
		var errs []string
		for i, root := range roots {
			prefix := root
			if len(roots) > 1 {
				prefix = fmt.Sprintf("(%d/%d) %s", i+1, len(roots), root)
			}
			fyne.Do(func() { status.SetText("Scanning " + prefix + " …") })
			groups, err := client.PlanAlbumImport(cfg, root, client.AlbumPlanOptions{
				DirType:  dirType,
				Template: template,
				ScanProgress: func(scanned, total int) {
					fyne.Do(func() {
						status.SetText(fmt.Sprintf("Scanning %s — %d / %d files probed", prefix, scanned, total))
					})
				},
			})
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", root, err))
				continue
			}
			plan = append(plan, groups...)
		}
		fyne.Do(func() {
			bar.Stop()
			scan.Hide()
			if len(errs) > 0 {
				dialog.ShowError(errors.New(strings.Join(errs, "\n")), fm.win)
			}
			if len(plan) == 0 {
				if len(errs) < len(roots) {
					dialog.ShowInformation("Import as albums",
						"No audio files or audio archives found in\n"+strings.Join(roots, "\n"), fm.win)
				}
				return
			}
			fm.showAlbumPlan(plan, dirType, tags)
		})
	}()
}

// showAlbumPlan presents the scanned import plan: one checkbox row per album
// with its destination, track count, size and warnings. Confirming imports
// exactly the checked groups. Groups without a destination (no template
// rendered, no album tag) cannot import and are fixed unchecked.
func (fm *FileManager) showAlbumPlan(plan []client.AlbumGroup, dirType string, tags []string) {
	selected := make([]bool, len(plan))
	for i, g := range plan {
		selected[i] = g.Dest != ""
	}

	summary := widget.NewLabel("")
	list := widget.NewList(
		func() int { return len(plan) },
		func() fyne.CanvasObject {
			check := widget.NewCheck("", nil)
			title := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			detail := widget.NewLabel("")
			detail.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, check, nil,
				container.NewVBox(title, detail))
		},
		func(i int, o fyne.CanvasObject) {
			// Match NewBorder's children by type, not position.
			var check *widget.Check
			var box *fyne.Container
			for _, ch := range o.(*fyne.Container).Objects {
				switch w := ch.(type) {
				case *widget.Check:
					check = w
				case *fyne.Container:
					box = w
				}
			}
			title := box.Objects[0].(*widget.Label)
			detail := box.Objects[1].(*widget.Label)

			g := plan[i]
			title.SetText(albumTitle(g))
			detail.SetText(albumDetail(g))
			check.OnChanged = func(on bool) {
				selected[i] = on
				summary.SetText(albumPlanSummary(plan, selected))
			}
			check.SetChecked(selected[i])
			if g.Dest == "" {
				check.Disable()
			} else {
				check.Enable()
			}
		})
	summary.SetText(albumPlanSummary(plan, selected))

	// A transparent spacer gives the list a usable size: a bare List reports a
	// tiny MinSize and the dialog would shrink to it.
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(640, 420))
	content := container.NewBorder(summary, nil, nil, nil,
		container.NewStack(spacer, list))
	dialog.ShowCustomConfirm("Import as albums", "Import", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		fm.runAlbumImport(selectedGroups(plan, selected), dirType, tags)
	}, fm.win)
}

// selectedGroups returns the plan groups whose checkbox is set.
func selectedGroups(plan []client.AlbumGroup, selected []bool) []client.AlbumGroup {
	var groups []client.AlbumGroup
	for i, g := range plan {
		if selected[i] {
			groups = append(groups, g)
		}
	}
	return groups
}

// runAlbumImport enqueues one op per confirmed album group. The ops queue is
// small and runs one op at a time, so feeding it blocks and happens off the UI
// goroutine. Completions are counted on the UI goroutine; when the batch has
// finished, a tie-browsing sibling panel reloads once and any per-group
// failures are summarized (one failed album does not abort the rest).
func (fm *FileManager) runAlbumImport(groups []client.AlbumGroup, dirType string, tags []string) {
	remaining := len(groups)
	var failed []string
	done := func(op *fs.Op) {
		fyne.Do(func() {
			if op.Err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", op.B.Path, op.Err))
			}
			remaining--
			if remaining > 0 {
				return
			}
			if fm.other != nil && fm.other.isTie() {
				fm.other.reload()
			}
			if len(failed) > 0 {
				dialog.ShowError(fmt.Errorf("%d album import(s) failed:\n%s",
					len(failed), strings.Join(failed, "\n")), fm.win)
			}
		})
	}
	go func() {
		for _, g := range groups {
			fm.ops.ImportAlbum(g, dirType, tags, done)
		}
	}()
}

// albumTitle is the plan row's headline: the album identity, falling back to
// the source directory name.
func albumTitle(g client.AlbumGroup) string {
	name := g.Artist
	if g.Album != "" {
		if name != "" {
			name += " — "
		}
		name += g.Album
	}
	if name == "" {
		name = filepath.Base(g.SourceDir)
	}
	if g.IsArchive {
		name += " (archive)"
	}
	return name
}

// albumDetail is the plan row's second line: destination, track count, size
// and any warnings (one line — the list row template is a fixed height).
func albumDetail(g client.AlbumGroup) string {
	dest := g.Dest
	if dest == "" {
		dest = "(no destination)"
	}
	d := fmt.Sprintf("→ %s · %d tracks · %s", dest, g.Tracks, humanizeBytes(g.Size))
	if len(g.Warnings) > 0 {
		d += " · ⚠ " + strings.Join(g.Warnings, "; ")
	}
	return d
}

// albumPlanSummary tallies the checked groups for the line above the list.
func albumPlanSummary(plan []client.AlbumGroup, selected []bool) string {
	n := 0
	var size int64
	for i, g := range plan {
		if selected[i] {
			n++
			size += g.Size
		}
	}
	return fmt.Sprintf("%d of %d albums selected · %s", n, len(plan), humanizeBytes(size))
}
