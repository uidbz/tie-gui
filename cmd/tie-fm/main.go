// Command tie-fm is a twin-panel file manager for local files and the tie
// tagging filesystem.
package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"

	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/config"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/devices"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/fs"
	"github.com/uidbz/tie-gui/cmd/tie-fm/internal/ui"
)

func main() {
	application := app.NewWithID("net.sourcehut.tie-fm")
	mainWin := application.NewWindow("tie-fm")

	appCfg, cfgErr := config.Load()
	if cfgErr != nil {
		appCfg = config.Default()
	}

	// The tie client is built from the configured tie config (a local server by
	// default) but only connects when a tie: path is visited; connection errors
	// surface as dialogs at that point.
	tieCfg, _ := config.LoadTieConfig(appCfg.TieConfig)
	tc := client.NewTieClient(tieCfg)

	registry := fs.NewRegistry(fs.NewLocalFS(), fs.NewTieFS(tc))

	// The MTP manager is shared: it serves the mtp: scheme (browsing/transfer)
	// and feeds the device sidebar (detection/eject).
	mtpMgr := fs.NewMTPManager()
	registry.SetMTP(fs.NewMTPFS(mtpMgr))
	devMgr := devices.NewManager(mtpMgr)

	ops := fs.NewOperations(registry)

	// The file-operations list lives at the bottom of the main window. A
	// transparent spacer fixes its height (a bare List reports a tiny MinSize),
	// and the whole panel is hidden while no operation is active so it collapses
	// when idle instead of leaving an empty strip.
	opsView := ui.NewFileOpView(ops)
	opsSpacer := canvas.NewRectangle(color.Transparent)
	opsSpacer.SetMinSize(fyne.NewSize(0, 120))
	opsPanel := container.NewBorder(
		widget.NewSeparator(), nil, nil, nil,
		container.NewStack(opsSpacer, opsView),
	)
	opsPanel.Hide()
	// content is assigned below; captured here so ActiveChanged can relayout it.
	var content *fyne.Container
	ops.ActiveChanged = func() {
		fyne.Do(func() {
			opsView.Refresh()
			if len(ops.Active()) > 0 {
				opsPanel.Show()
			} else {
				opsPanel.Hide()
			}
			// Container.Show() only clears the Hidden flag; it does not relayout
			// the parent, so the border never reclaims the bottom strip. Refresh
			// the content border to re-run its layout.
			if content != nil {
				content.Refresh()
			}
		})
	}

	left := ui.NewFileManager(homeStart(appCfg), registry, ops, &appCfg, mainWin)
	right := ui.NewFileManager(homeStart(appCfg), registry, ops, &appCfg, mainWin)
	left.SetSibling(right)
	right.SetSibling(left)

	// One panel is "active" at a time (highlighted); interacting with a panel
	// makes it active. Shared actions (bookmark navigation, add-bookmark) target
	// the active panel.
	active := left
	setActive := func(fm *ui.FileManager) {
		active = fm
		left.SetActive(fm == left)
		right.SetActive(fm == right)
	}
	left.SetOnActive(setActive)
	right.SetOnActive(setActive)
	setActive(left)

	twin := container.NewHSplit(left.View(), right.View())

	sidebar := widget.NewList(
		func() int { return len(appCfg.Bookmarks) },
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.FolderIcon()), widget.NewLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			objs := o.(*fyne.Container).Objects
			bm := appCfg.Bookmarks[i]
			objs[0].(*widget.Icon).SetResource(bookmarkIcon(bm.Path))
			objs[1].(*widget.Label).SetText(bm.Label)
		})
	sidebar.OnSelected = func(id int) {
		if id >= 0 && id < len(appCfg.Bookmarks) {
			active.NavigateTo(appCfg.Bookmarks[id].Path)
		}
		sidebar.UnselectAll()
	}

	// Each panel's toolbar bookmark button targets that panel's current path
	// (rather than only the active panel, which the shared menu entry uses).
	addBookmarkPath := func(path string) { addBookmark(mainWin, &appCfg, path, sidebar) }
	left.SetBookmarkHandler(addBookmarkPath)
	right.SetBookmarkHandler(addBookmarkPath)

	// The Devices section lists connected removable storage (USB block devices
	// and MTP phones/cameras). It updates live as devices come and go; clicking a
	// row mounts (if needed) and navigates the active panel; the eject button
	// unmounts.
	var devSnap []devices.Device
	devicesList := widget.NewList(
		func() int { return len(devSnap) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil,
				widget.NewIcon(theme.StorageIcon()),
				widget.NewButtonWithIcon("", theme.LogoutIcon(), nil),
				widget.NewLabel("template"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(devSnap) {
				return
			}
			d := devSnap[i]
			objs := o.(*fyne.Container).Objects // border order: center, then left, right
			objs[0].(*widget.Label).SetText(d.Label)
			objs[1].(*widget.Icon).SetResource(deviceIcon(d.Kind))
			eject := objs[2].(*widget.Button)
			eject.OnTapped = func() {
				go func() {
					if err := devMgr.Eject(d); err != nil {
						fyne.Do(func() { dialog.ShowError(err, mainWin) })
					}
				}()
			}
		})
	devicesList.OnSelected = func(id int) {
		devicesList.UnselectAll()
		if id < 0 || id >= len(devSnap) {
			return
		}
		d := devSnap[id]
		go func() {
			p, err := devMgr.Mount(d)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, mainWin)
					return
				}
				active.NavigateTo(p)
			})
		}()
	}

	devicesHeader := widget.NewLabelWithStyle("Devices", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	devSpacer := canvas.NewRectangle(color.Transparent)
	devSpacer.SetMinSize(fyne.NewSize(0, 150))
	devicesBox := container.NewBorder(devicesHeader, nil, nil, nil,
		container.NewStack(devSpacer, devicesList))
	devicesBox.Hide()

	leftPane := container.NewBorder(nil, devicesBox, nil, nil, sidebar)

	// applyTieConfig rebuilds the tie client from path, persists the choice, and
	// reloads both panels so any open tie: view refreshes.
	applyTieConfig := func(path string) {
		cfg, err := config.LoadTieConfig(path)
		if err != nil {
			dialog.ShowError(err, mainWin)
			return
		}
		registry.SetTie(fs.NewTieFS(client.NewTieClient(cfg)))
		appCfg.TieConfig = path
		if err := appCfg.Save(); err != nil {
			dialog.ShowError(err, mainWin)
		}
		left.Reload()
		right.Reload()
	}

	// A native main menu is intentionally avoided: on Linux Fyne draws it as an
	// overlay that the desktop's Alt+RightMouse resize gesture pops open (the Alt
	// release toggles the menu). The same actions are exposed via an in-app menu
	// button instead.
	appMenu := fyne.NewMenu("tie-fm",
		fyne.NewMenuItem("Select tie config…", func() {
			d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
				if err != nil || rc == nil {
					return
				}
				defer rc.Close()
				applyTieConfig(rc.URI().Path())
			}, mainWin)
			d.SetFilter(storage.NewExtensionFileFilter([]string{".toml"}))
			d.Show()
		}),
		fyne.NewMenuItem("Use default tie config (local server)", func() {
			applyTieConfig("")
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Add current location to bookmarks", func() {
			addBookmark(mainWin, &appCfg, active.CurrentPath(), sidebar)
		}),
		fyne.NewMenuItem("Manage bookmarks…", func() {
			manageBookmarks(mainWin, &appCfg, sidebar)
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("File associations…", func() {
			ui.ShowFileAssociations(mainWin, &appCfg)
		}),
	)

	menuBtn := widget.NewButtonWithIcon("Menu", theme.MenuIcon(), nil)
	menuBtn.Alignment = widget.ButtonAlignLeading
	menuBtn.OnTapped = func() {
		pop := widget.NewPopUpMenu(appMenu, mainWin.Canvas())
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(menuBtn)
		pop.ShowAtPosition(pos.Add(fyne.NewPos(0, menuBtn.Size().Height)))
	}
	topBar := container.NewHBox(menuBtn)

	content = container.NewBorder(topBar, opsPanel, leftPane, nil, twin)

	// Rebuild the devices list on hot-plug; hide the whole section when empty so
	// it does not reserve space. Show()/Hide() only toggles the flag, so refresh
	// the content border to re-run its layout (mirrors the ops panel).
	devMgr.OnChange = func() {
		fyne.Do(func() {
			devSnap = devMgr.Devices()
			devicesList.Refresh()
			if len(devSnap) > 0 {
				devicesBox.Show()
			} else {
				devicesBox.Hide()
			}
			content.Refresh()
		})
	}
	// Start in a goroutine: the initial scan fires OnChange, whose fyne.Do must
	// queue onto the main loop rather than run before ShowAndRun starts it.
	go devMgr.Start()

	mainWin.SetContent(content)
	mainWin.Resize(fyne.NewSize(1100, 700))
	mainWin.SetMaster()
	mainWin.ShowAndRun()
}

// homeStart returns the path the panels should open on: the first non-tie
// bookmark, else "/".
func homeStart(cfg config.Config) string {
	for _, bm := range cfg.Bookmarks {
		if !fs.IsTie(bm.Path) {
			return bm.Path
		}
	}
	return "/"
}

func bookmarkIcon(path string) fyne.Resource {
	if fs.IsTie(path) {
		return theme.StorageIcon()
	}
	return theme.FolderIcon()
}

func deviceIcon(k devices.Kind) fyne.Resource {
	if k == devices.MTP {
		return theme.ComputerIcon()
	}
	return theme.StorageIcon()
}

// addBookmark appends a bookmark for path (prompting for a label) and persists.
func addBookmark(win fyne.Window, cfg *config.Config, path string, sidebar *widget.List) {
	entry := widget.NewEntry()
	entry.SetText(defaultLabel(path))
	dialog.ShowForm("Add bookmark", "Add", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Label", entry),
			widget.NewFormItem("Path", widget.NewLabel(path)),
		},
		func(ok bool) {
			if !ok {
				return
			}
			cfg.Bookmarks = append(cfg.Bookmarks, config.Bookmark{Label: entry.Text, Path: path})
			if err := cfg.Save(); err != nil {
				dialog.ShowError(err, win)
			}
			sidebar.Refresh()
		}, win)
}

// manageBookmarks shows an editable list of bookmarks. Each row can be
// reordered with the up/down buttons or by dragging the grip on its left edge,
// and removed with the delete button.
func manageBookmarks(win fyne.Window, cfg *config.Config, sidebar *widget.List) {
	list := container.NewVBox()

	// dragFrom is the index of the row being dragged (-1 when idle); dragY is
	// the pointer's latest absolute Y during the drag, used to pick the drop
	// index on release.
	dragFrom := -1
	var dragY float32

	// The row-building helpers are mutually recursive, so declare them before
	// assigning so each can reference the others regardless of order.
	var persist func()
	var moveTo func(from, to int)
	var remove func(i int)
	var dropIndex func(y float32) int
	var makeRow func(i int) fyne.CanvasObject
	var rebuild func()

	persist = func() {
		if err := cfg.Save(); err != nil {
			dialog.ShowError(err, win)
		}
		sidebar.Refresh()
	}

	moveTo = func(from, to int) {
		n := len(cfg.Bookmarks)
		if from < 0 || from >= n || to < 0 || to >= n || from == to {
			return
		}
		cfg.Bookmarks = moveBookmark(cfg.Bookmarks, from, to)
		persist()
		rebuild()
	}

	remove = func(i int) {
		if i < 0 || i >= len(cfg.Bookmarks) {
			return
		}
		cfg.Bookmarks = append(cfg.Bookmarks[:i], cfg.Bookmarks[i+1:]...)
		persist()
		rebuild()
	}

	// dropIndex maps a pointer Y to the row whose vertical slot it is over,
	// i.e. the final index a dragged bookmark would occupy when dropped there.
	dropIndex = func(y float32) int {
		rows := list.Objects
		if len(rows) == 0 {
			return 0
		}
		for i, row := range rows {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(row)
			if y < pos.Y+row.Size().Height/2 {
				return i
			}
		}
		return len(rows) - 1
	}

	makeRow = func(i int) fyne.CanvasObject {
		bm := cfg.Bookmarks[i]
		label := widget.NewLabel(bm.Label + "  (" + bm.Path + ")")
		label.Truncation = fyne.TextTruncateEllipsis

		up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() { moveTo(i, i-1) })
		down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() { moveTo(i, i+1) })
		del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { remove(i) })
		if i == 0 {
			up.Disable()
		}
		if i == len(cfg.Bookmarks)-1 {
			down.Disable()
		}

		handle := newDragHandle(
			func(e *fyne.DragEvent) {
				dragFrom = i
				dragY = e.AbsolutePosition.Y
			},
			func() {
				from := dragFrom
				dragFrom = -1
				if from < 0 {
					return
				}
				moveTo(from, dropIndex(dragY))
			},
		)

		return container.NewBorder(nil, nil, handle,
			container.NewHBox(up, down, del), label)
	}

	rebuild = func() {
		objs := make([]fyne.CanvasObject, 0, len(cfg.Bookmarks))
		for i := range cfg.Bookmarks {
			objs = append(objs, makeRow(i))
		}
		list.Objects = objs
		list.Refresh()
	}

	rebuild()

	d := dialog.NewCustom("Bookmarks", "Close", container.NewVScroll(list), win)
	d.Resize(fyne.NewSize(460, 320))
	d.Show()
}

// moveBookmark returns b with the element at `from` relocated to final index
// `to`. Both indices are bounds-checked; a no-op returns b unchanged.
func moveBookmark(b []config.Bookmark, from, to int) []config.Bookmark {
	if from == to || from < 0 || from >= len(b) || to < 0 || to >= len(b) {
		return b
	}
	item := b[from]
	out := make([]config.Bookmark, 0, len(b))
	for i, x := range b {
		if i == from {
			continue
		}
		out = append(out, x)
	}
	out = append(out, item)
	copy(out[to+1:], out[to:])
	out[to] = item
	return out
}

// dragHandle is a small grip that starts a drag gesture for its row. It
// implements fyne.Draggable; onDragged receives the live drag events and
// onDropped fires once when the gesture ends.
type dragHandle struct {
	widget.BaseWidget
	onDragged func(*fyne.DragEvent)
	onDropped func()
}

func newDragHandle(onDragged func(*fyne.DragEvent), onDropped func()) *dragHandle {
	h := &dragHandle{onDragged: onDragged, onDropped: onDropped}
	h.ExtendBaseWidget(h)
	return h
}

func (h *dragHandle) Dragged(e *fyne.DragEvent) {
	if h.onDragged != nil {
		h.onDragged(e)
	}
}

func (h *dragHandle) DragEnd() {
	if h.onDropped != nil {
		h.onDropped()
	}
}

func (h *dragHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(widget.NewIcon(theme.MenuIcon()))
}

func defaultLabel(path string) string {
	if fs.IsTie(path) {
		return "tie"
	}
	if path == "" || path == "/" {
		return "/"
	}
	name := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			name = path[i+1:]
			break
		}
	}
	if name == "" {
		return path
	}
	return name
}
