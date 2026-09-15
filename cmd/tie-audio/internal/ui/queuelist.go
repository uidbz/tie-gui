package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
)

// Row heights for the grouped playlist, in Fyne device-independent pixels.
const (
	queueAlbumRowHeight = 72
	queueTrackRowHeight = 48
)

// queueRowKind distinguishes the two row shapes in the grouped playlist.
type queueRowKind int

const (
	queueRowAlbum queueRowKind = iota // album header with cover art
	queueRowTrack                     // one playable track
)

// queueRow is one rendered row of the grouped playlist: either an album header
// or a track belonging to the header above it.
type queueRow struct {
	kind     queueRowKind
	albumUID string
	title    string
	subtitle string
	// trackIndex is the row's position in the backend playlist (track rows).
	// It is -1 for headers.
	trackIndex int
	// groupStart / groupCount describe the run of playlist entries an album
	// header covers, so album-level actions (play, remove, move) can address
	// the block without re-deriving it.
	groupStart int
	groupCount int
	// duration is the track's playing time (track rows), 0 when unknown.
	duration float64
	trackNo  int
}

// buildQueueRows groups a queue into album headers followed by their tracks.
// Grouping is by *consecutive* runs of the same album: the queue is an ordered
// play list, so the same album appearing twice at different positions is two
// groups, which is what the user sees and reorders.
//
// Tracks tie knows no album for are grouped by their album tag instead, and
// tracks with neither are collected under one "Unknown album" run, so a queue
// of loose files still renders as a list rather than one header per track.
func buildQueueRows(tracks []data.Track) []queueRow {
	var rows []queueRow
	groupKey := func(t data.Track) string {
		if t.AlbumUID != "" {
			return "uid:" + t.AlbumUID
		}
		if t.Album != "" {
			return "album:" + t.Album
		}
		return "unknown"
	}

	for i := 0; i < len(tracks); {
		key := groupKey(tracks[i])
		end := i + 1
		for end < len(tracks) && groupKey(tracks[end]) == key {
			end++
		}
		group := tracks[i:end]
		header := queueRow{
			kind:       queueRowAlbum,
			albumUID:   group[0].AlbumUID,
			title:      queueGroupTitle(group[0]),
			subtitle:   queueGroupSubtitle(group),
			trackIndex: -1,
			groupStart: i,
			groupCount: len(group),
		}
		rows = append(rows, header)
		for j, t := range group {
			rows = append(rows, queueRow{
				kind:       queueRowTrack,
				albumUID:   t.AlbumUID,
				title:      t.Display(),
				subtitle:   t.Artist,
				trackIndex: i + j,
				groupStart: i,
				groupCount: len(group),
				duration:   t.Duration,
				trackNo:    t.TrackNo,
			})
		}
		i = end
	}
	return rows
}

// queueGroupTitle names an album run from its first track.
func queueGroupTitle(t data.Track) string {
	if t.Album != "" {
		return t.Album
	}
	return "Unknown album"
}

// queueGroupSubtitle summarises an album run: its artist (when every track
// agrees), its year, and the track count.
func queueGroupSubtitle(group []data.Track) string {
	artist := commonArtist(group)
	year := ""
	for _, t := range group {
		if t.Year != "" {
			year = t.Year
			break
		}
	}
	parts := make([]string, 0, 3)
	if artist != "" {
		parts = append(parts, artist)
	}
	if year != "" {
		parts = append(parts, year)
	}
	parts = append(parts, fmt.Sprintf("%d %s", len(group), plural(len(group), "track", "tracks")))
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// queueList is the compact layout's playlist: a widget.List of album headers
// and track rows instead of a table. A table needs several columns of width to
// be worth anything, and on a phone there is only room for one — so the queue
// becomes a list that spends that width on the album grouping and cover art
// the table columns cannot show.
//
// Interactions: tap a track to play it, long-press for its menu, use the drag
// handle to reorder, and the header's ⋮ for album-level actions.
type queueList struct {
	page *queuePage

	list   *widget.List
	object fyne.CanvasObject
	rows   []queueRow
	// current is the playing row's playlist index, mirrored from the page so
	// rows can mark themselves.
	current int
}

func newQueueList(page *queuePage) *queueList {
	q := &queueList{page: page, current: -1}
	q.list = widget.NewList(
		func() int { return len(q.rows) },
		func() fyne.CanvasObject { return newQueueListRow(q) },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			row, ok := obj.(*queueListRow)
			if !ok || id < 0 || id >= len(q.rows) {
				return
			}
			row.set(q.rows[id], q.rows[id].trackIndex == q.current)
		},
	)
	q.object = q.list
	return q
}

func (q *queueList) Object() fyne.CanvasObject { return q.object }

// setTracks rebuilds the rows from the live queue. Called from the same
// rebuildTracks that feeds the table, so both stay in step.
func (q *queueList) setTracks(tracks []data.Track, current int) {
	q.rows = buildQueueRows(tracks)
	q.current = current
	q.list.Refresh()
	// Heights must be re-applied after a rebuild: an index that was a header
	// before may now be a track.
	for i, row := range q.rows {
		if row.kind == queueRowAlbum {
			q.list.SetItemHeight(i, queueAlbumRowHeight)
		} else {
			q.list.SetItemHeight(i, queueTrackRowHeight)
		}
	}
}

// queueListRow renders either shape of row. One widget serves both so the list
// can recycle items freely: the unused variant is simply hidden.
type queueListRow struct {
	widget.BaseWidget
	owner *queueList

	row queueRow

	// album header
	cover       *coverCell
	albumTitle  *widget.Label
	albumDetail *widget.Label
	albumMenu   *widget.Button
	albumBox    *fyne.Container

	// track row
	indicator *widget.Label
	trackNo   *widget.Label
	title     *widget.Label
	subtitle  *widget.Label
	duration  *widget.Label
	handle    *queueDragHandle
	trackBox  *fyne.Container
}

func newQueueListRow(owner *queueList) *queueListRow {
	r := &queueListRow{owner: owner}

	r.cover = newCoverCell(owner.page.covers)
	r.albumTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	r.albumTitle.Truncation = fyne.TextTruncateEllipsis
	r.albumDetail = widget.NewLabel("")
	r.albumDetail.Truncation = fyne.TextTruncateEllipsis
	r.albumMenu = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() { r.showAlbumMenu() })
	r.albumMenu.Importance = widget.LowImportance
	r.albumBox = container.NewBorder(nil, nil,
		container.NewGridWrap(fyne.NewSize(groupCoverSize, groupCoverSize), r.cover),
		r.albumMenu,
		container.NewVBox(r.albumTitle, r.albumDetail),
	)

	r.indicator = widget.NewLabel("")
	r.trackNo = widget.NewLabel("")
	r.title = widget.NewLabel("")
	r.title.Truncation = fyne.TextTruncateEllipsis
	r.subtitle = widget.NewLabel("")
	r.subtitle.Truncation = fyne.TextTruncateEllipsis
	r.duration = widget.NewLabel("")
	r.handle = newQueueDragHandle(r)
	r.trackBox = container.NewBorder(nil, nil,
		container.NewHBox(r.indicator, r.trackNo),
		container.NewHBox(r.duration, r.handle),
		container.NewVBox(r.title, r.subtitle),
	)

	r.ExtendBaseWidget(r)
	return r
}

// set renders one row, showing the matching variant.
func (r *queueListRow) set(row queueRow, playing bool) {
	r.row = row
	if row.kind == queueRowAlbum {
		r.albumBox.Show()
		r.trackBox.Hide()
		r.albumTitle.SetText(row.title)
		r.albumDetail.SetText(row.subtitle)
		r.cover.show(row.albumUID)
		return
	}
	r.albumBox.Hide()
	r.trackBox.Show()
	if playing {
		r.indicator.SetText("▶")
	} else {
		r.indicator.SetText(" ")
	}
	if row.trackNo > 0 {
		r.trackNo.SetText(fmt.Sprintf("%d.", row.trackNo))
	} else {
		r.trackNo.SetText("")
	}
	r.title.SetText(row.title)
	r.subtitle.SetText(row.subtitle)
	if row.duration > 0 {
		r.duration.SetText(formatDuration(row.duration))
	} else {
		r.duration.SetText("")
	}
}

// Tapped plays the tapped track. Album headers ignore taps: their actions are
// in the ⋮ menu, so a stray tap on a header cannot restart the album.
func (r *queueListRow) Tapped(_ *fyne.PointEvent) {
	if r.row.kind == queueRowTrack {
		r.owner.page.playRow(r.row.trackIndex)
	}
}

// TappedSecondary opens the row's menu. On mobile Fyne delivers this on a long
// press, which is the gesture the compact playlist documents.
func (r *queueListRow) TappedSecondary(e *fyne.PointEvent) {
	if r.row.kind == queueRowAlbum {
		r.owner.page.showAlbumRowMenu(r.row, e.AbsolutePosition)
		return
	}
	r.owner.page.showTrackRowMenu(r.row, e.AbsolutePosition)
}

// showAlbumMenu opens the album menu at the ⋮ button.
func (r *queueListRow) showAlbumMenu() {
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(r.albumMenu)
	r.owner.page.showAlbumRowMenu(r.row, pos)
}

func (r *queueListRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.albumBox, r.trackBox))
}

// queueDragHandle is the ≡ grip that reorders a row. Dragging is confined to
// the handle on purpose: a drag starting anywhere on the row would fight the
// list's own scrolling, which on a touch screen is the same gesture.
type queueDragHandle struct {
	widget.BaseWidget
	row *queueListRow

	dragging  bool
	startY    float32
	fromIndex int
	ghost     *widget.PopUp
	target    int
}

func newQueueDragHandle(row *queueListRow) *queueDragHandle {
	h := &queueDragHandle{row: row, target: -1}
	h.ExtendBaseWidget(h)
	return h
}

func (h *queueDragHandle) Dragged(e *fyne.DragEvent) {
	page := h.row.owner.page
	row := h.row.row
	if row.kind != queueRowTrack {
		return
	}
	if !h.dragging {
		h.dragging = true
		h.startY = e.AbsolutePosition.Y
		h.fromIndex = row.trackIndex
		h.target = row.trackIndex
		page.setDragging(true)
	}

	// Track rows are a fixed height, so the travelled distance converts
	// directly to a number of queue positions. Album headers make the on-screen
	// distance longer than the queue distance, which just makes the drag feel
	// slightly slow across group boundaries — acceptable next to the complexity
	// of mapping pixel offsets through a variable-height list.
	moved := int((e.AbsolutePosition.Y - h.startY) / queueTrackRowHeight)
	target := h.fromIndex + moved
	if target < 0 {
		target = 0
	}
	if max := page.queueLen() - 1; target > max {
		target = max
	}
	h.target = target

	label := fmt.Sprintf("%s → %d", row.title, target+1)
	if h.ghost == nil {
		lbl := widget.NewLabel(label)
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		h.ghost = widget.NewPopUp(lbl, page.win.Canvas())
	} else if lbl, ok := h.ghost.Content.(*widget.Label); ok {
		lbl.SetText(label)
	}
	h.ghost.ShowAtPosition(e.AbsolutePosition.AddXY(-120, 8))
}

func (h *queueDragHandle) DragEnd() {
	if !h.dragging {
		return
	}
	h.dragging = false
	if h.ghost != nil {
		h.ghost.Hide()
		h.ghost = nil
	}
	page := h.row.owner.page
	page.setDragging(false)
	if h.target >= 0 && h.target != h.fromIndex {
		page.moveTrack(h.fromIndex, h.target)
	}
	h.target = -1
}

func (h *queueDragHandle) CreateRenderer() fyne.WidgetRenderer {
	icon := canvas.NewText("≡", theme.Color(theme.ColorNameForeground))
	icon.TextSize = theme.TextSize() * 1.2
	return widget.NewSimpleRenderer(container.NewCenter(icon))
}

func (h *queueDragHandle) MinSize() fyne.Size {
	// Wide enough to grab with a thumb without crowding the duration label.
	return fyne.NewSize(36, queueTrackRowHeight-8)
}
