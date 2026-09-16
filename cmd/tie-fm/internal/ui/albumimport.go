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
)

// importAsAlbums starts a bulk album import of a local directory into tie: the
// directory is scanned and clustered into albums (client.PlanAlbumImport), the
// user reviews the plan (destinations, track counts, warnings), and the
// confirmed groups import through the ops engine, each landing at its rendered
// destination verbatim (Operations.ImportAlbum).
func (fm *FileManager) importAsAlbums(e fs.Entry) {
	fm.markActive()
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
	tagsEntry := widget.NewEntry()
	tagsEntry.PlaceHolder = "comma-separated tags (optional)"

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

	dialog.ShowForm("Import as albums: "+e.Name, "Scan", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Directory type", typeSel),
			widget.NewFormItem("Custom", custom),
			widget.NewFormItem("Destination", tmplEntry),
			widget.NewFormItem("", remember),
			widget.NewFormItem("Tags", tagsEntry),
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
			fm.scanAlbumPlan(e, tc.Config, dirType, tmpl, splitTags(tagsEntry.Text))
		}, fm.win)
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

// splitTags parses a comma-separated tag list, trimming spaces and dropping
// empties.
func splitTags(s string) []string {
	var tags []string
	for tag := range strings.SplitSeq(s, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// scanAlbumPlan runs the album planner off the UI goroutine — probing a large
// or network-mounted library can take minutes, so progress is shown. The
// finished plan (or the scan error) returns to the UI goroutine via fyne.Do.
// template overrides the tie config's ImportDest entry for dirType (an empty
// string falls back to the config, then to source-path placement).
func (fm *FileManager) scanAlbumPlan(e fs.Entry, cfg client.Config, dirType, template string, tags []string) {
	root := strings.TrimPrefix(e.Path, "file:")
	status := widget.NewLabel("Scanning " + root + " …")
	bar := widget.NewProgressBarInfinite()
	scan := dialog.NewCustomWithoutButtons("Import as albums",
		container.NewVBox(status, bar), fm.win)
	scan.Show()
	go func() {
		plan, err := client.PlanAlbumImport(cfg, root, client.AlbumPlanOptions{
			DirType:  dirType,
			Template: template,
			ScanProgress: func(scanned, total int) {
				fyne.Do(func() {
					status.SetText(fmt.Sprintf("Scanning %s — %d / %d files probed", root, scanned, total))
				})
			},
		})
		fyne.Do(func() {
			bar.Stop()
			scan.Hide()
			if err != nil {
				dialog.ShowError(err, fm.win)
				return
			}
			if len(plan) == 0 {
				dialog.ShowInformation("Import as albums",
					"No audio files or audio archives found in\n"+root, fm.win)
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
