package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/musicbrainz"
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
	matchMB := widget.NewCheck("Look up releases on MusicBrainz", nil)
	matchMB.SetChecked(fm.cfg.MusicBrainz.AutoMatch)
	coverArt := widget.NewCheck("Download missing cover art", nil)
	coverArt.SetChecked(fm.cfg.MusicBrainz.FetchCoverArt)

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
			widget.NewFormItem("", matchMB),
			widget.NewFormItem("", coverArt),
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
			fm.scanAlbumPlan(entries, tc.Config, dirType, tmpl, tags, matchMB.Checked, coverArt.Checked)
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
func (fm *FileManager) scanAlbumPlan(entries []fs.Entry, cfg client.Config, dirType, template string, tags []string, matchMB, coverArt bool) {
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
		var matches map[string]*musicbrainz.Release
		if matchMB && len(plan) > 0 {
			fyne.Do(func() { status.SetText("Matching albums on MusicBrainz…") })
			matches = fm.matchAlbumPlan(plan, template, func(msg string) {
				fyne.Do(func() { status.SetText(msg) })
			})
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
			fm.showAlbumPlan(plan, dirType, tags, template, matches, coverArt)
		})
	}()
}

// showAlbumPlan presents the scanned import plan: one checkbox row per album
// with its destination, track count, size and warnings, plus a MusicBrainz
// button to pick/change the release match. Confirming imports exactly the
// checked groups. Groups without a destination (no template rendered, no album
// tag) cannot import and are fixed unchecked.
func (fm *FileManager) showAlbumPlan(plan []client.AlbumGroup, dirType string, tags []string, template string, matches map[string]*musicbrainz.Release, coverArt bool) {
	if matches == nil {
		matches = map[string]*musicbrainz.Release{}
	}
	selected := make([]bool, len(plan))
	for i, g := range plan {
		selected[i] = g.Dest != ""
	}

	summary := widget.NewLabel("")
	var list *widget.List
	list = widget.NewList(
		func() int { return len(plan) },
		func() fyne.CanvasObject {
			check := widget.NewCheck("", nil)
			btn := widget.NewButton("MB…", nil)
			btn.Importance = widget.LowImportance
			title := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			detail := widget.NewLabel("")
			detail.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, check, btn,
				container.NewVBox(title, detail))
		},
		func(i int, o fyne.CanvasObject) {
			// Match NewBorder's children by type, not position.
			var check *widget.Check
			var btn *widget.Button
			var box *fyne.Container
			for _, ch := range o.(*fyne.Container).Objects {
				switch w := ch.(type) {
				case *widget.Check:
					check = w
				case *widget.Button:
					btn = w
				case *fyne.Container:
					box = w
				}
			}
			title := box.Objects[0].(*widget.Label)
			detail := box.Objects[1].(*widget.Label)

			idx := i
			g := plan[i]
			title.SetText(albumTitle(g))
			detail.SetText(albumDetail(g, matches[g.SourceDir]))
			check.OnChanged = func(on bool) {
				selected[idx] = on
				summary.SetText(albumPlanSummary(plan, selected, matches))
			}
			check.SetChecked(selected[idx])
			if g.Dest == "" {
				check.Disable()
			} else {
				check.Enable()
			}
			if g.IsArchive {
				btn.Disable()
			}
			btn.OnTapped = func() {
				g := &plan[idx]
				q := musicbrainz.ReleaseQuery{
					Artist:     g.Artist,
					Album:      g.Album,
					Year:       g.Year,
					TrackCount: g.Tracks,
				}
				fm.showReleasePicker(q, matches[g.SourceDir], func(rel *musicbrainz.Release) {
					matches[g.SourceDir] = rel
					applyReleaseToGroup(g, rel, template)
					selected[idx] = g.Dest != ""
					list.Refresh()
					summary.SetText(albumPlanSummary(plan, selected, matches))
				})
			}
		})
	summary.SetText(albumPlanSummary(plan, selected, matches))

	// A transparent spacer gives the list a usable size: a bare List reports a
	// tiny MinSize and the dialog would shrink to it.
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(720, 420))
	content := container.NewBorder(summary, nil, nil, nil,
		container.NewStack(spacer, list))
	dialog.ShowCustomConfirm("Import as albums", "Import", "Cancel", content, func(ok bool) {
		if !ok {
			return
		}
		groups := selectedGroups(plan, selected)
		picked := map[string]*musicbrainz.Release{}
		for _, g := range groups {
			if rel := matches[g.SourceDir]; rel != nil {
				picked[g.SourceDir] = rel
			}
		}
		fm.runAlbumImport(groups, dirType, tags, picked, coverArt)
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

// matchAlbumPlan searches MusicBrainz for every planned album, applies the best
// match to the group (artist/album/year and destination), and returns the
// chosen releases keyed by SourceDir.
func (fm *FileManager) matchAlbumPlan(plan []client.AlbumGroup, template string, status func(string)) map[string]*musicbrainz.Release {
	out := map[string]*musicbrainz.Release{}
	p, err := openMusicBrainz(fm.cfg.MusicBrainz)
	if err != nil {
		status("MusicBrainz unavailable: " + err.Error())
		return out
	}
	defer p.Close()
	for i := range plan {
		g := &plan[i]
		if g.IsArchive {
			// Archive groups have no readable track list, so per-track
			// metadata cannot be applied; keep their source placement.
			continue
		}
		q := musicbrainz.ReleaseQuery{
			Artist:     g.Artist,
			Album:      g.Album,
			Year:       g.Year,
			TrackCount: g.Tracks,
			Limit:      5,
		}
		if q.Artist == "" && q.Album == "" {
			continue
		}
		status(fmt.Sprintf("MusicBrainz: matching %d/%d — %s", i+1, len(plan), albumTitle(*g)))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rels, err := p.SearchReleases(ctx, q)
		cancel()
		if err != nil {
			status("MusicBrainz: " + err.Error())
			continue
		}
		if len(rels) > 0 && rels[0].Score >= 0.55 {
			rel := rels[0]
			applyReleaseToGroup(g, &rel, template)
			out[g.SourceDir] = &rel
		}
	}
	status(fmt.Sprintf("MusicBrainz: matched %d of %d albums", len(out), len(plan)))
	return out
}

// applyReleaseToGroup rewrites the plan group's album identity from a matched
// release and re-renders its destination (when a template is in use).
func applyReleaseToGroup(g *client.AlbumGroup, rel *musicbrainz.Release, template string) {
	if rel == nil {
		return
	}
	if artist := rel.DisplayArtist(); artist != "" {
		g.Artist = artist
	}
	if rel.Title != "" {
		g.Album = rel.Title
	}
	if rel.Year > 0 {
		g.Year = rel.Year
	}
	if dest, ok := renderAlbumDest(template, g.Artist, g.Album, g.Year); ok {
		g.Dest = dest
	}
}

// renderAlbumDest renders the album-level destination template with tie's
// rules (values stay within one path segment). ok is false when the template
// is empty or references a value the album does not provide; the planner's
// destination is then kept untouched.
func renderAlbumDest(template, artist, album string, year int) (string, bool) {
	if strings.TrimSpace(template) == "" {
		return "", false
	}
	values := map[string]string{
		"artist":      sanitizePathSegment(artist),
		"albumartist": sanitizePathSegment(artist),
		"album":       sanitizePathSegment(album),
		"title":       sanitizePathSegment(album),
		"year":        "",
		"track":       "",
	}
	if year > 0 {
		values["year"] = strconv.Itoa(year)
	}
	var b strings.Builder
	for i := 0; i < len(template); {
		if template[i] != '{' {
			b.WriteByte(template[i])
			i++
			continue
		}
		end := strings.IndexByte(template[i:], '}')
		if end < 0 {
			return "", false
		}
		name := template[i+1 : i+end]
		v, ok := values[name]
		if !ok || v == "" {
			return "", false
		}
		b.WriteString(v)
		i += end + 1
	}
	return b.String(), true
}

// runAlbumImport enqueues one op per confirmed album group. The ops queue is
// small and runs one op at a time, so feeding it blocks and happens off the UI
// goroutine. Completions are counted on the UI goroutine; when the batch has
// finished, a tie-browsing sibling panel reloads once, any MusicBrainz matches
// are applied to the imported albums, and per-group failures are summarized
// (one failed album does not abort the rest).
func (fm *FileManager) runAlbumImport(groups []client.AlbumGroup, dirType string, tags []string, matches map[string]*musicbrainz.Release, coverArt bool) {
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
			fm.afterAlbumImports(groups, matches, coverArt, failed)
		})
	}
	go func() {
		for _, g := range groups {
			fm.ops.ImportAlbum(g, dirType, tags, done)
		}
	}()
}

// afterAlbumImports reloads the tie panes and applies MusicBrainz metadata to
// the imported albums (one background pass, then a single summary dialog).
func (fm *FileManager) afterAlbumImports(groups []client.AlbumGroup, matches map[string]*musicbrainz.Release, coverArt bool, failed []string) {
	if fm.other != nil && fm.other.isTie() {
		fm.other.reload()
	}
	if len(matches) == 0 {
		if len(failed) > 0 {
			dialog.ShowError(fmt.Errorf("%d album import(s) failed:\n%s",
				len(failed), strings.Join(failed, "\n")), fm.win)
		}
		return
	}
	tc, err := fm.tieClient()
	if err != nil {
		failed = append(failed, err.Error())
		if len(failed) > 0 {
			dialog.ShowError(fmt.Errorf("%d album import(s) failed:\n%s",
				len(failed), strings.Join(failed, "\n")), fm.win)
		}
		return
	}
	cfg := fm.cfg.MusicBrainz
	progLabel := widget.NewLabel("Applying MusicBrainz tags…")
	prog := dialog.NewCustomWithoutButtons("MusicBrainz",
		container.NewVBox(progLabel, widget.NewProgressBarInfinite()), fm.win)
	prog.Show()
	go func() {
		var provider musicbrainz.Provider
		for _, g := range groups {
			rel := matches[g.SourceDir]
			if rel == nil {
				continue
			}
			label := "MusicBrainz: " + albumTitle(g)
			fyne.Do(func() { progLabel.SetText(label) })
			full := rel
			var aerr error
			if !rel.HasTracks() {
				if provider == nil {
					provider, aerr = openMusicBrainz(cfg)
					if aerr != nil {
						failed = append(failed, fmt.Sprintf("%s: %v", g.Dest, aerr))
						continue
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				full, aerr = provider.LookupRelease(ctx, rel.MBID)
				cancel()
			}
			if aerr == nil {
				aerr = applyReleaseToAlbum(tc, "tie:"+g.Dest, full, coverArt, cfg)
			}
			if aerr != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", g.Dest, aerr))
			}
		}
		if provider != nil {
			provider.Close()
		}
		fyne.Do(func() {
			prog.Hide()
			fm.reload()
			if fm.other != nil && fm.other.isTie() {
				fm.other.reload()
			}
			if len(failed) > 0 {
				dialog.ShowError(fmt.Errorf("%d MusicBrainz problem(s):\n%s",
					len(failed), strings.Join(failed, "\n")), fm.win)
			}
		})
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
func albumDetail(g client.AlbumGroup, rel *musicbrainz.Release) string {
	dest := g.Dest
	if dest == "" {
		dest = "(no destination)"
	}
	d := fmt.Sprintf("→ %s · %d tracks · %s", dest, g.Tracks, humanizeBytes(g.Size))
	if rel != nil {
		d += " · MB: " + musicBrainzNoticeText(rel)
	}
	if len(g.Warnings) > 0 {
		d += " · ⚠ " + strings.Join(g.Warnings, "; ")
	}
	return d
}

// albumPlanSummary tallies the checked groups for the line above the list.
func albumPlanSummary(plan []client.AlbumGroup, selected []bool, matches map[string]*musicbrainz.Release) string {
	n, matched := 0, 0
	var size int64
	for i, g := range plan {
		if selected[i] {
			n++
			size += g.Size
			if matches[g.SourceDir] != nil {
				matched++
			}
		}
	}
	s := fmt.Sprintf("%d of %d albums selected · %s", n, len(plan), humanizeBytes(size))
	if len(matches) > 0 {
		s += fmt.Sprintf(" · %d of %d matched on MusicBrainz", matched, n)
	}
	return s
}
