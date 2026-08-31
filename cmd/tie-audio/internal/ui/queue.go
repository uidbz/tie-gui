package ui

import (
	"fmt"
	"math/rand"
	"path"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// queuePage renders the live playback queue (pwplay's playlist) using the same
// column-customizable table as the album view, with a leading column marking
// the currently-playing track. Rows drag to reorder (→ MoveItems), and the
// toolbar offers shuffle, a repeat-all toggle, and save-as-playlist. It stays
// live by subscribing to the transport bar's status poll instead of running its
// own ticker, so all state mutation happens on the UI goroutine.
//
// Header-click sorting is intentionally disabled: the queue carries an
// intrinsic play order that drag-reorder and MoveItems operate on, and the
// currently-playing index is a backend row — a sorted view would desync all
// three.
type queuePage struct {
	win       fyne.Window
	session   *data.Session
	backend   playback.PlaybackBackend
	transport *transportBar
	back      func() // restore the album cover wall (mobile back button)
	mobile    bool

	table     *trackTable
	repeatBtn *widget.Button
	object    fyne.CanvasObject

	// snapshot of the last status, read by the table cell callbacks. Mutated
	// only on the UI goroutine. qtracks is the table's row model, rebuilt from
	// playlist URLs via the transport's URL→Track registry.
	playlist []string
	qtracks  []data.Track
	current  int
	dragging bool

	// dragGhost is a floating label that follows the cursor during a reorder
	// drag so the user can see which track they picked up. Created on the first
	// drag-move event and dismissed on release.
	dragFrom  int
	dragGhost *widget.PopUp
	// dragRows is the set of rows the current drag carries: the whole selection
	// when the grabbed row is part of it, else just the grabbed row. Captured at
	// drag start and consumed on release.
	dragRows []int
}

// newQueuePage builds the queue view once; show() (re)binds it to the live poll.
func newQueuePage(win fyne.Window, session *data.Session, transport *transportBar, mobile bool, back func(), onColumnsChanged func([]string)) *queuePage {
	q := &queuePage{win: win, session: session, backend: session.Backend, transport: transport, mobile: mobile, back: back, current: -1}

	q.table = newTrackTable(win, nil, session.Cfg.QueueColumns,
		nil, // single-tap selects (multiSelect); double-tap plays via onDoubleTap
		onColumnsChanged,
		trackTableOpts{
			indicator:   q.rowIndicator,
			onReorder:   q.onReorder,
			onDragStart: q.onDragStart,
			onDragMove:  q.onDragMove,
			onDoubleTap: q.playRow,
			multiSelect: true,
		},
	)

	content := container.NewBorder(q.buildToolbar(), nil, nil, nil, q.table.object)
	if mobile {
		// A left-edge rightward swipe returns to the cover wall (mirrors the
		// gallery's left→right swipe); the strip sits above the content but only
		// occupies the left edge, leaving the table free to scroll.
		strip := newEdgeSwipe(q.leave)
		q.object = container.NewStack(content, container.NewBorder(nil, nil, strip, nil, nil))
	} else {
		q.object = content
	}
	return q
}

// rowIndicator returns the play glyph for the current row, else "". A text glyph
// (not a widget.Icon) because Icon.SetResource(nil) does not reliably repaint
// inside a recycled table cell — the old play icon lingers on rows the current
// track has moved past. Labels repaint correctly here.
func (q *queuePage) rowIndicator(row int) string {
	if row == q.current {
		return "▶" // ▶
	}
	return ""
}

// buildToolbar builds the top row: shuffle, repeat, save, columns (and a back
// button on mobile, where the queue is a full-screen view).
func (q *queuePage) buildToolbar() fyne.CanvasObject {
	shuffle := widget.NewButtonWithIcon("Shuffle", theme.MediaReplayIcon(), q.shuffle)
	save := widget.NewButtonWithIcon("Save as playlist", theme.DocumentSaveIcon(), q.saveQueue)
	clear := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), q.clearPlaylist)
	columns := widget.NewButtonWithIcon("Columns", theme.MenuIcon(), q.table.showColumnsDialog)
	q.repeatBtn = widget.NewButton("", q.toggleRepeat)
	q.refreshRepeatLabel()
	title := widget.NewLabelWithStyle("Playlist", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	buttons := container.NewHBox(shuffle, q.repeatBtn, save, clear, columns)
	if q.mobile {
		back := widget.NewButtonWithIcon("Albums", theme.NavigateBackIcon(), q.leave)
		buttons = container.NewHBox(back, shuffle, q.repeatBtn, save, clear, columns)
	}
	return container.NewVBox(buttons, title, widget.NewSeparator())
}

// show binds the page to the transport's status poll and triggers an immediate
// refresh so the view is populated without waiting a poll interval.
func (q *queuePage) show() {
	q.table.show() // size columns now that the canvas width is known
	q.transport.SetStatusListener(q.applyStatus)
	go func() {
		s, err := q.backend.Status()
		if err != nil {
			return
		}
		fyne.Do(func() { q.applyStatus(s) })
	}()
}

// hide unsubscribes from the poll (mobile, when swiping back to the wall).
func (q *queuePage) hide() {
	q.transport.SetStatusListener(nil)
}

// leave unsubscribes from the poll and returns to the cover wall.
func (q *queuePage) leave() {
	q.hide()
	if q.back != nil {
		q.back()
	}
}

// applyStatus refreshes the table from a status snapshot. Runs on the UI
// goroutine (the transport invokes its listener there). It skips refreshing
// mid-drag so a rebuild does not disrupt the gesture.
func (q *queuePage) applyStatus(s playback.Status) {
	if q.dragging {
		return
	}
	q.playlist = append(q.playlist[:0], s.Playlist...)
	q.current = s.CurrentTrack
	q.rebuildTracks()
}

// rebuildTracks resolves each playlist URL to its registered track metadata,
// falling back to a stub whose Display() is the URL's last segment, then pushes
// the slice into the shared table (which refreshes).
func (q *queuePage) rebuildTracks() {
	q.qtracks = q.qtracks[:0]
	for _, url := range q.playlist {
		if m, ok := q.transport.TrackMeta(url); ok {
			q.qtracks = append(q.qtracks, m)
		} else {
			q.qtracks = append(q.qtracks, data.Track{Filename: path.Base(url)})
		}
	}
	q.table.setTracks(q.qtracks)
}

// onDragStart records the grabbed row and the rows the drag carries (the whole
// selection when the grabbed row is selected, else just that row), and marks the
// drag guard so status polls don't rebuild the table mid-gesture.
func (q *queuePage) onDragStart(row int) {
	q.dragging = true
	q.dragFrom = row
	q.dragRows = []int{row}
	for _, r := range q.table.selectedRows() {
		if r == row {
			q.dragRows = q.table.selectedRows()
			break
		}
	}
}

// onDragMove shows/moves a floating label under the cursor naming the track
// being dragged (or the count, for a multi-row drag), so the reorder gesture has
// visible feedback.
func (q *queuePage) onDragMove(pos fyne.Position) {
	if q.dragGhost == nil {
		text := q.trackLabel(q.dragFrom)
		if len(q.dragRows) > 1 {
			text = fmt.Sprintf("%d tracks", len(q.dragRows))
		}
		lbl := widget.NewLabel(text)
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		q.dragGhost = widget.NewPopUp(lbl, q.win.Canvas())
	}
	q.dragGhost.ShowAtPosition(pos.AddXY(12, 8))
}

// clearDragGhost dismisses the floating drag label if present.
func (q *queuePage) clearDragGhost() {
	if q.dragGhost != nil {
		q.dragGhost.Hide()
		q.dragGhost = nil
	}
}

// dropLineAt draws the insertion indicator for an external (album-cover) drag at
// pos and returns the playlist gap it points to.
func (q *queuePage) dropLineAt(pos fyne.Position) int { return q.table.showInsertionLineAt(pos) }

// gapAt returns the playlist gap under pos without drawing anything.
func (q *queuePage) gapAt(pos fyne.Position) int { return q.table.gapAt(pos) }

// clearDropLine hides the external-drag insertion indicator.
func (q *queuePage) clearDropLine() { q.table.hideInsertionLine() }

// insertTracksAt registers the tracks' metadata and inserts their stream URLs at
// playlist position gap. The rows appear at the drop point on the next status
// poll, matching the append path (enqueueTracks), so there is no optimistic
// local edit to reconcile.
func (q *queuePage) insertTracksAt(gap int, urls []string, meta []data.Track) {
	if len(urls) == 0 {
		return
	}
	q.transport.AppendQueue(urls, meta)
	go func() {
		if err := q.backend.Insert(gap, urls...); err != nil {
			fyne.Do(func() { dialog.ShowError(err, q.win) })
		}
	}()
}

// onReorder moves the dragged rows locally for instant feedback, then commits
// the change to the backend; the next poll reconciles any drift. A single
// grabbed row is one MoveItems; a multi-row selection is diffed into a sequence
// of single-item moves that reproduce the new order.
func (q *queuePage) onReorder(from, to int) {
	q.dragging = false
	q.clearDragGhost()
	sel := q.dragRows
	q.dragRows = nil
	n := len(q.playlist)

	if from < 0 || to < 0 || from >= n || to >= n || from == to {
		q.rebuildTracks()
		return
	}

	if len(sel) <= 1 {
		moveOne(q.playlist, from, to)
		q.current = shiftIndexOnMove(q.current, from, to)
		q.rebuildTracks()
		q.commit([][2]int{{from, to}})
		return
	}

	// Recover the insertion gap the drop landed at from the grabbed row's final
	// index (the inverse of the widget's single-row gap→index conversion), so the
	// moved block's top aligns with the drawn drop line.
	gap := to
	if to > from {
		gap = to + 1
	}
	want := reorderSelection(n, sel, gap)
	if want == nil {
		q.rebuildTracks()
		return
	}
	moves := reorderMoves(want)
	np := make([]string, n)
	oldCur := q.current
	for i, idx := range want {
		np[i] = q.playlist[idx]
		if idx == oldCur {
			q.current = i
		}
	}
	q.playlist = np
	q.table.clearSelection()
	q.rebuildTracks()
	q.commit(moves)
}

// playRow jumps playback to the double-tapped queue position.
func (q *queuePage) playRow(row int) {
	if row < 0 || row >= len(q.playlist) {
		return
	}
	go func() {
		if err := q.backend.Goto(row); err != nil {
			fyne.Do(func() { dialog.ShowError(err, q.win) })
		}
	}()
}

// commit applies a sequence of single-item moves to the backend on a background
// goroutine, stopping at the first error. Each pair is (from, to) for
// MoveItems(from, 1, to).
func (q *queuePage) commit(moves [][2]int) {
	if len(moves) == 0 {
		return
	}
	go func() {
		for _, m := range moves {
			if err := q.backend.MoveItems(m[0], 1, m[1]); err != nil {
				fyne.Do(func() { dialog.ShowError(err, q.win) })
				return
			}
		}
	}()
}

// shuffle randomizes the order of the tracks after the current one (so the
// playing track keeps playing), committing each step with MoveItems. pwplay has
// no shuffle call, so this emulates one client-side.
func (q *queuePage) shuffle() {
	n := len(q.playlist)
	start := q.current + 1
	if start < 0 {
		start = 0
	}
	if n-start < 2 {
		return // nothing meaningful to shuffle
	}
	// Selection shuffle: for each target slot, swap in a random remaining track.
	moves := make([][2]int, 0, n-start)
	for pos := start; pos < n; pos++ {
		r := pos + rand.Intn(n-pos)
		if r != pos {
			moveOne(q.playlist, r, pos)
			moves = append(moves, [2]int{r, pos})
		}
	}
	q.rebuildTracks()
	go func() {
		for _, m := range moves {
			if err := q.backend.MoveItems(m[0], 1, m[1]); err != nil {
				fyne.Do(func() { dialog.ShowError(err, q.win) })
				return
			}
		}
	}()
}

// saveQueue prompts for a name and persists the current queue as a tie playlist
// (an ordered audio-dir), so it reappears as an album tile under the playlist tag.
func (q *queuePage) saveQueue() {
	if len(q.playlist) == 0 {
		dialog.ShowInformation("Save playlist", "The playlist is empty.", q.win)
		return
	}
	hashes := make([]string, len(q.playlist))
	for i, u := range q.playlist {
		hashes[i] = path.Base(u)
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Playlist name")
	form := []*widget.FormItem{widget.NewFormItem("Name", entry)}
	dialog.ShowForm("Save as playlist", "Save", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		go func() {
			_, err := q.session.SaveQueueAsPlaylist(entry.Text, hashes)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, q.win)
					return
				}
				dialog.ShowInformation("Playlist saved",
					fmt.Sprintf("Saved %d tracks as %q.", len(hashes), entry.Text), q.win)
			})
		}()
	}, q.win)
}

// clearPlaylist empties the queue after a confirmation prompt. The next status
// poll reflects the now-empty playlist (and stopped playback).
func (q *queuePage) clearPlaylist() {
	if len(q.playlist) == 0 {
		return
	}
	dialog.ShowConfirm("Clear playlist", "Remove all tracks from the playlist?", func(ok bool) {
		if !ok {
			return
		}
		go func() {
			if err := q.backend.Clear(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, q.win) })
			}
		}()
	}, q.win)
}

// toggleRepeat flips repeat-all on the transport (which owns the behavior so it
// works even when the queue view is not shown).
func (q *queuePage) toggleRepeat() {
	q.transport.SetRepeat(!q.transport.RepeatAll())
	q.refreshRepeatLabel()
}

func (q *queuePage) refreshRepeatLabel() {
	if q.transport.RepeatAll() {
		q.repeatBtn.SetText("Repeat: All")
		q.repeatBtn.Importance = widget.HighImportance
	} else {
		q.repeatBtn.SetText("Repeat: Off")
		q.repeatBtn.Importance = widget.MediumImportance
	}
	q.repeatBtn.Refresh()
}

// trackLabel resolves the display title for queue position i, preferring the
// transport's URL→track registry and falling back to the URL's last segment.
func (q *queuePage) trackLabel(i int) string {
	if i < 0 || i >= len(q.playlist) {
		return ""
	}
	url := q.playlist[i]
	if lbl := q.transport.Label(url); lbl != "" {
		return lbl
	}
	return path.Base(url)
}

// reorderSelection computes the new queue order (as a permutation of the
// original indices 0..n-1) after dragging a multi-row selection so the block's
// top lands at insertion gap `gap` (rows above the gap, in [0, n]). The selected
// rows move as one block in their original relative order; everything else keeps
// its order around them. sel must be sorted ascending. Returns nil if the result
// is unchanged.
func reorderSelection(n int, sel []int, gap int) []int {
	inSel := make(map[int]bool, len(sel))
	for _, r := range sel {
		inSel[r] = true
	}
	rest := make([]int, 0, n-len(sel))
	for i := 0; i < n; i++ {
		if !inSel[i] {
			rest = append(rest, i)
		}
	}
	// blockStart is how many non-selected rows precede the gap; the block slots in
	// right after them so its top sits at the drawn boundary.
	blockStart := 0
	for _, r := range rest {
		if r < gap {
			blockStart++
		}
	}
	want := make([]int, 0, n)
	want = append(want, rest[:blockStart]...)
	want = append(want, sel...)
	want = append(want, rest[blockStart:]...)
	// No-op guard: unchanged order means nothing to commit.
	for i := 0; i < n; i++ {
		if want[i] != i {
			return want
		}
	}
	return nil
}

// reorderMoves turns a target permutation of 0..n-1 into a sequence of
// single-item moves (from, to) that transform the identity order into want.
// Each move maps directly to MoveItems(from, 1, to). At most n-1 moves.
func reorderMoves(want []int) [][2]int {
	n := len(want)
	work := make([]int, n)
	for i := range work {
		work[i] = i
	}
	var moves [][2]int
	for i := 0; i < n; i++ {
		j := i
		for work[j] != want[i] {
			j++
		}
		if j != i {
			moveOneInt(work, j, i)
			moves = append(moves, [2]int{j, i})
		}
	}
	return moves
}

// moveOneInt is moveOne for an int slice.
func moveOneInt(s []int, from, to int) {
	v := s[from]
	if from < to {
		copy(s[from:to], s[from+1:to+1])
	} else {
		copy(s[to+1:from+1], s[to:from])
	}
	s[to] = v
}

// moveOne relocates the element at index from so it ends at index to, shifting
// the intervening elements. from and to must be valid indices into s.
func moveOne(s []string, from, to int) {
	v := s[from]
	if from < to {
		copy(s[from:to], s[from+1:to+1])
	} else {
		copy(s[to+1:from+1], s[to:from])
	}
	s[to] = v
}

// shiftIndexOnMove returns where an element at index idx lands after the element
// at from is moved to to (mirrors pwplay's currentTrack bookkeeping).
func shiftIndexOnMove(idx, from, to int) int {
	switch {
	case idx == from:
		return to
	case from < idx && idx <= to:
		return idx - 1
	case to <= idx && idx < from:
		return idx + 1
	default:
		return idx
	}
}
