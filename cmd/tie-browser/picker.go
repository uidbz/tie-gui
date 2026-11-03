package main

import (
	"context"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/fynerisor/gui/guithread"
	"github.com/uidbz/fynerisor/scene"
)

// pickAsset implements designer.Options.AssetPick. Media properties get a
// thumbnail grid over a tag query; scene references get a path entry.
func (t *TieBrowser) pickAsset(kind scene.PropKind, current string, done func(string, bool)) {
	if t.window == nil || t.window.FyneWindow == nil {
		done("", false)
		return
	}
	if kind == scene.PropSceneRef {
		t.pickSceneRef(current, done)
		return
	}
	t.pickMedia(current, done)
}

func (t *TieBrowser) pickSceneRef(current string, done func(string, bool)) {
	entry := widget.NewEntry()
	entry.SetText(strings.TrimPrefix(current, "tie:"))
	entry.SetPlaceHolder("tie:/scenes/house/living-room")
	var dlg *dialog.CustomDialog
	ok := widget.NewButton("Select", func() {
		ref := strings.TrimSpace(entry.Text)
		if ref == "" {
			return
		}
		if !strings.HasPrefix(ref, "tie:") {
			ref = "tie:" + ref
		}
		dlg.Hide()
		done(ref, true)
	})
	dlg = dialog.NewCustom("Pick a scene", "Cancel", container.NewBorder(nil, nil, nil, ok,
		container.NewVBox(widget.NewLabel("Scene directory path"), entry)), t.window.FyneWindow)
	dlg.Resize(fyne.NewSize(520, 180))
	dlg.Show()
}

func (t *TieBrowser) pickMedia(current string, done func(string, bool)) {
	var (
		items []client.TaggedFile
		dlg   *dialog.CustomDialog
	)
	// loaded maps a cell's image to the hash it currently shows. GridWrap
	// recycles cells, and a resource's Name() is the filehost hash with a
	// ".bin" suffix, so comparing names never matched and every update
	// reloaded (and briefly blanked) the thumbnail.
	loaded := make(map[*canvas.Image]string)
	gw := widget.NewGridWrap(
		func() int { return len(items) },
		func() fyne.CanvasObject {
			img := canvas.NewImageFromResource(nil)
			img.FillMode = canvas.ImageFillCover
			return container.NewGridWrap(fyne.NewSize(96, 96), img)
		},
		func(id widget.GridWrapItemID, obj fyne.CanvasObject) {
			if id < 0 || int(id) >= len(items) {
				return
			}
			box, ok := obj.(*fyne.Container)
			if !ok || len(box.Objects) == 0 {
				return
			}
			img, ok := box.Objects[0].(*canvas.Image)
			if !ok {
				return
			}
			hash := items[id].Hash
			if loaded[img] == hash {
				return
			}
			loaded[img] = hash
			img.Resource = nil
			img.Refresh()
			go func() {
				res, err := t.assets.Open(context.Background(), "tie:"+hash)
				guithread.Do(func() {
					if err != nil || loaded[img] != hash {
						return // recycled while loading: do not show the wrong item
					}
					img.Resource = res
					img.Refresh()
				})
			}()
		},
	)
	gw.OnSelected = func(id widget.GridWrapItemID) {
		if id < 0 || int(id) >= len(items) {
			return
		}
		ref := "tie:" + items[id].Hash
		if dlg != nil {
			dlg.Hide()
		}
		done(ref, true)
	}
	query := widget.NewEntry()
	placeholder := "tag query, e.g. family 2014 -private"
	if cur := strings.TrimPrefix(current, "tie:"); cur != "" {
		// The current value is a content hash, not a tag query: pre-filling it
		// searched for a tag that never exists.
		placeholder = "tag query (current: " + cur[:min(8, len(cur))] + "…)"
	}
	query.SetPlaceHolder(placeholder)
	search := widget.NewButton("Search", func() {
		include, exclude := splitQuery(query.Text)
		go func() {
			rows, _, err := t.tc.FilesWithTags("", include, exclude, 0, 100)
			guithread.Do(func() {
				if err != nil {
					t.browser.SetStatus("Search failed: " + err.Error())
					return
				}
				items = items[:0]
				for _, f := range rows {
					if !f.IsDir && !f.IsArchive {
						items = append(items, f)
					}
				}
				gw.Refresh()
				gw.ScrollToTop()
			})
		}()
	})
	query.OnSubmitted = func(string) { search.OnTapped() }
	content := container.NewBorder(
		container.NewVBox(widget.NewLabel("Tag query"), container.NewBorder(nil, nil, nil, search, query)),
		nil, nil, nil,
		container.NewScroll(gw),
	)
	dlg = dialog.NewCustom("Pick tie media", "Cancel", content, t.window.FyneWindow)
	dlg.Resize(fyne.NewSize(760, 520))
	dlg.Show()
}

func splitQuery(s string) (include, exclude []string) {
	for _, part := range strings.Fields(s) {
		if strings.HasPrefix(part, "-") && len(part) > 1 {
			exclude = append(exclude, part[1:])
		} else {
			include = append(include, part)
		}
	}
	return
}
