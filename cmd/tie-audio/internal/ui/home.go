package ui

// home.go renders tie-audio's Home page (the "landing page"): a vertical
// list of shelves built by data.BuildHome — jump back in, recently played
// tracks, recently added, "because you played …", haven't heard in a while,
// on this day, favorites, never played, playlists — each a horizontal row of
// cover tiles. It is a feed of the browse page (feedHome), so the wall's
// navigation (showWall / showBrowse / reloadWall, Back from an album) treats
// it like the cover grid or the album table.

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/gallery"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// homeMaxAge is how long a built Home page is reused before showing it
// rebuilds it (new plays also invalidate it).
const homeMaxAge = 5 * time.Minute

// homeView is the Home page.
type homeView struct {
	page    *browsePage
	object  fyne.CanvasObject
	shelves *fyne.Container
	scroll  *container.Scroll
	status  *widget.Label

	home    data.Home
	built   time.Time
	stale   bool
	loading bool
	// gen drops a build that finished after a newer one was requested (a
	// collection switch while loading).
	gen int
	// buildFn builds the page off the UI goroutine; tests substitute it.
	buildFn func() (data.Home, error)
}

func newHomeView(b *browsePage) *homeView {
	h := &homeView{page: b, stale: true}
	h.status = widget.NewLabel("")
	h.status.Wrapping = fyne.TextWrapWord
	h.shelves = container.NewVBox()
	h.scroll = container.NewVScroll(h.shelves)
	title := widget.NewLabelWithStyle("Home", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	reload := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), h.reload)
	reload.Importance = widget.LowImportance
	albums := widget.NewButtonWithIcon("All albums", theme.GridIcon(), b.showLatest)
	albums.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, title, container.NewHBox(albums, reload))
	h.object = container.NewBorder(header, nil, nil, nil, h.scroll)
	return h
}

// invalidate marks the page for a rebuild the next time it is shown (new
// plays were recorded, a collection switch).
func (h *homeView) invalidate() { h.stale = true }

// reset drops the page entirely (collection switch): nothing from the old
// collection may be shown or opened.
func (h *homeView) reset() {
	h.gen++
	h.home = data.Home{}
	h.built = time.Time{}
	h.stale = true
	h.loading = false
	h.render()
}

// ensure rebuilds the page if it is stale or old; otherwise it is left as is
// (same scroll position).
func (h *homeView) ensure() {
	if h.stale || time.Since(h.built) > homeMaxAge {
		h.load()
	}
}

// reload rebuilds the page now (the header's refresh button, pull-to-refresh
// equivalents, "Reload albums").
func (h *homeView) reload() {
	h.page.covers.Clear()
	h.load()
}

func (h *homeView) load() {
	if h.loading {
		return
	}
	h.loading = true
	h.stale = false
	h.gen++
	gen := h.gen
	if len(h.home.Shelves) == 0 {
		h.status.SetText("Loading…")
		h.render()
	}
	build := h.buildFn
	if build == nil {
		sess := h.page.session
		build = func() (data.Home, error) { return sess.Home(time.Now()) }
	}
	go func() {
		home, err := build()
		fyne.Do(func() {
			if gen != h.gen {
				return
			}
			h.loading = false
			if err != nil {
				h.status.SetText("Could not load the home page: " + err.Error())
				h.home = data.Home{}
				h.render()
				return
			}
			h.home = home
			h.built = time.Now()
			h.status.SetText("")
			h.render()
		})
	}()
}

// tileEdge is a Home tile's cover size.
func (h *homeView) tileEdge() float32 {
	if h.page.compact {
		return 124
	}
	return 156
}

func (h *homeView) render() {
	objs := []fyne.CanvasObject{}
	if h.status.Text != "" {
		objs = append(objs, h.status)
	}
	if len(h.home.Shelves) == 0 && h.status.Text == "" {
		h.status.SetText("Nothing here yet. Import albums (tie-fm \"Import as albums…\" or " +
			"`tie import audio-dir --albums`) and play some music: this page then shows what is new, " +
			"what you played lately, related albums, and old favorites you have not heard in a while.")
		objs = append(objs, h.status)
	}
	for _, s := range h.home.Shelves {
		objs = append(objs, h.shelfObject(s))
	}
	h.shelves.Objects = objs
	h.shelves.Refresh()
}

func (h *homeView) shelfObject(s data.Shelf) fyne.CanvasObject {
	title := widget.NewLabelWithStyle(s.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Truncation = fyne.TextTruncateEllipsis
	var right fyne.CanvasObject = layout.NewSpacer()
	if len(s.Albums) > 0 {
		shelf := s
		all := widget.NewButton("See all", func() { h.page.showAlbumList(shelf.Title, shelf.Albums) })
		all.Importance = widget.LowImportance
		right = all
	}
	head := container.NewBorder(nil, nil, nil, right, title)
	var rows []fyne.CanvasObject
	rows = append(rows, head)
	if s.Subtitle != "" {
		sub := widget.NewLabel(s.Subtitle)
		sub.Importance = widget.LowImportance
		sub.Truncation = fyne.TextTruncateEllipsis
		rows = append(rows, sub)
	}
	var tiles []fyne.CanvasObject
	for _, a := range s.Albums {
		tiles = append(tiles, h.albumTile(a))
	}
	for _, t := range s.Tracks {
		tiles = append(tiles, h.trackTile(t))
	}
	row := container.NewHScroll(container.NewHBox(tiles...))
	rows = append(rows, row)
	return container.NewVBox(append(rows, widget.NewSeparator())...)
}

func (h *homeView) albumTile(a data.Album) fyne.CanvasObject {
	sub := a.Artist
	if a.Kind == data.AlbumPlaylist {
		sub = fmt.Sprintf("Playlist · %d tracks", a.TrackCount)
	}
	album := a
	return newHomeTile(h.page.covers, a.UID, a.Display(), sub, h.tileEdge(),
		func() { h.page.openAlbum(album) },
		func(obj fyne.CanvasObject) { h.page.showAlbumMenu(obj, album) })
}

func (h *homeView) trackTile(t data.Track) fyne.CanvasObject {
	track := t
	album := h.albumFor(t)
	return newHomeTile(h.page.covers, t.AlbumUID, t.Display(), t.Artist, h.tileEdge(),
		func() { h.page.playFromAlbum(album, track) },
		func(obj fyne.CanvasObject) {
			menu := fyne.NewMenu("",
				fyne.NewMenuItem("Play from album", func() { h.page.playFromAlbum(album, track) }),
				fyne.NewMenuItem("Play track", func() { h.page.playTracks([]data.Track{track}, 0) }),
				fyne.NewMenuItem("Add track to playlist", func() { h.page.enqueueTracks([]data.Track{track}) }),
				fyne.NewMenuItem("Open album", func() { h.page.openAlbum(album) }),
			)
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(obj)
			widget.ShowPopUpMenuAtPosition(menu, h.page.win.Canvas(), pos)
		})
}

// albumFor resolves the album a track tile belongs to.
func (h *homeView) albumFor(t data.Track) data.Album {
	if a, ok := h.home.ByUID[t.AlbumUID]; ok {
		return a
	}
	if t.AlbumUID == "" || t.AlbumUID == t.Hash {
		return data.Album{UID: t.Hash, Kind: data.AlbumTrack, Title: t.Display()}
	}
	return data.Album{UID: t.AlbumUID, Kind: data.AlbumDir, Title: t.Album}
}

// homeTile is one cover tile: art, title, subtitle; tap opens, secondary tap
// (right click / long press) shows actions.
type homeTile struct {
	widget.BaseWidget
	content   fyne.CanvasObject
	onTap     func()
	onMenu    func(fyne.CanvasObject)
	hoverRect *canvas.Rectangle
}

func newHomeTile(store *coverStore, albumUID, title, subtitle string, edge float32, onTap func(), onMenu func(fyne.CanvasObject)) *homeTile {
	cell := newCoverCell(store)
	cell.show(albumUID)
	art := container.New(layout.NewGridWrapLayout(fyne.NewSize(edge, edge)), cell)
	t := widget.NewLabel(title)
	t.Truncation = fyne.TextTruncateEllipsis
	t.TextStyle = fyne.TextStyle{Bold: true}
	s := widget.NewLabel(subtitle)
	s.Truncation = fyne.TextTruncateEllipsis
	s.Importance = widget.LowImportance
	text := container.New(&tightVBox{}, t, s)
	body := container.NewBorder(art, nil, nil, nil, text)
	tile := &homeTile{onTap: onTap, onMenu: onMenu}
	tile.hoverRect = canvas.NewRectangle(theme.Color(theme.ColorNameHover))
	tile.hoverRect.Hide()
	width := container.New(layout.NewGridWrapLayout(fyne.NewSize(edge, edge+tightVBoxHeight(t, s))), body)
	tile.content = container.NewStack(tile.hoverRect, width)
	tile.ExtendBaseWidget(tile)
	return tile
}

func (t *homeTile) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.content) }

func (t *homeTile) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *homeTile) TappedSecondary(*fyne.PointEvent) {
	if t.onMenu != nil {
		t.onMenu(t)
	}
}

func (t *homeTile) MouseIn(*desktop.MouseEvent)    { t.hoverRect.Show(); t.hoverRect.Refresh() }
func (t *homeTile) MouseMoved(*desktop.MouseEvent) {}
func (t *homeTile) MouseOut()                      { t.hoverRect.Hide(); t.hoverRect.Refresh() }

// tightVBox stacks labels without the theme padding between them, so a
// tile's two text lines sit close under the cover.
type tightVBox struct{}

func (tightVBox) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		m := o.MinSize()
		w = max(w, m.Width)
		h += m.Height - theme.Padding()
	}
	return fyne.NewSize(w, h)
}

func (tightVBox) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, o := range objs {
		m := o.MinSize()
		o.Move(fyne.NewPos(0, y-theme.Padding()/2))
		o.Resize(fyne.NewSize(size.Width, m.Height))
		y += m.Height - theme.Padding()
	}
}

func tightVBoxHeight(objs ...fyne.CanvasObject) float32 {
	return tightVBox{}.MinSize(objs).Height
}

// --- browse page integration ---

// homeRoot composes the Home view like the table view: beside the sidebar
// in the regular layout, under the sidebar drawer in the compact one.
func (b *browsePage) homeRoot() fyne.CanvasObject {
	body := b.home.object
	if !b.compact {
		if sidebar := b.viewer.Sidebar; sidebar != nil {
			split := container.NewHSplit(sidebar, body)
			split.SetOffset(0.2)
			return split
		}
		return body
	}
	if drawer := b.viewer.DrawerObject(); drawer != nil {
		return container.NewStack(body, drawer)
	}
	return body
}

// showHome switches the wall to the Home page (clearing the tag selection,
// which no longer narrows what is shown).
func (b *browsePage) showHome() {
	if b.home == nil {
		b.home = newHomeView(b)
	}
	if b.feed != feedHome {
		if in, ex := b.ts.SelectedTags(); len(in) > 0 || len(ex) > 0 {
			b.ts.SetSelected(nil)
			b.updateFilterChips()
			go b.refineTags(nil, nil)
		}
	}
	b.fsTree.mu.Lock()
	b.fsTree.currentDir = ""
	b.fsTree.mu.Unlock()
	b.feed = feedHome
	b.albumOpen = false
	b.showHomeView()
}

// showHomeView pushes the Home page to the window, rebuilding it when stale.
func (b *browsePage) showHomeView() {
	if b.home == nil {
		b.home = newHomeView(b)
	}
	b.win.SetContent(b.homeRoot())
	b.home.ensure()
	if b.onFeedChanged != nil {
		b.onFeedChanged()
	}
}

// showAlbumList feeds the wall with a fixed list of albums (a Home shelf's
// "See all").
func (b *browsePage) showAlbumList(_ string, albums []data.Album) {
	b.feed = feedList
	b.albumOpen = false
	list := append([]data.Album(nil), albums...)
	b.setWallAlbums(list)
	b.viewer.ReadCustomAsync(func() []gallery.CustomReader { return b.readers(list) })
	b.showWall()
}

// playFromAlbum plays track's album starting at track (a Home track tile).
func (b *browsePage) playFromAlbum(a data.Album, t data.Track) {
	go func() {
		tracks, err := b.session.AlbumTracks(a)
		fyne.Do(func() {
			if err != nil || len(tracks) == 0 {
				b.playTracks([]data.Track{t}, 0)
				return
			}
			start := 0
			for i, x := range tracks {
				if x.Hash == t.Hash {
					start = i
					break
				}
			}
			b.playTracks(tracks, start)
		})
	}()
}
