package ui

import (
	"fmt"
	"math/rand"
	"path"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/data"
	"github.com/uidbz/tie-gui/cmd/tie-audio/internal/playback"
)

// queuePage renders the live playback queue (pwplay's playlist). It has two
// renderings of the same state:
//
//   - the regular layout uses the column-customizable table shared with the
//     album view (leading play indicator, cover column, drag-reorder,
//     multi-select);
//   - the compact layout uses queueList, a phone-shaped list grouped by album
//     with cover headers, because a multi-column table does not fit a phone.
//
// Both are fed by rebuildTracks, so switching between them (a window resize
// across the compact threshold) needs no re-sync. The page stays live by
// subscribing to the player's status poll instead of running its own ticker, so
// all state mutation happens on the UI goroutine.
//
// Header-click sorting is intentionally disabled: the queue carries an
// intrinsic play order that drag-reorder and MoveItems operate on, and the
// currently-playing index is a backend row — a sorted view would desync all
// three.
type queuePage struct {
	win       fyne.Window
	session   *data.Session
	transport *player
	covers    *coverStore
	back      func() // restore the album cover wall (compact back button)
	// compact selects the grouped-list rendering over the table.
	compact bool

	table     *trackTable
	list      *queueList
	repeatBtn *widget.Button
	// toolbarHolder / bodyHolder are swapped in place when the layout mode
	// changes, so the page object handed to the shell stays the same.
	toolbarHolder *fyne.Container
	bodyHolder    *fyne.Container
	object        *fyne.Container

	// snapshot of the last status, read by the table cell callbacks. Mutated
	// only on the UI goroutine. qtracks is the table's row model, rebuilt from
	// playlist URLs via the player's URL→Track registry.
	playlist []string
	qtracks  []data.Track
	current  int
	dragging bool
	// pending counts queue-mutating backend operations (enqueue, play-album)
	// started by the user but not yet settled server-side. While one is in
	// flight, status polls are skipped: pwplay applies queue mutations
	// asynchronously, so a poll landing mid-mutation would briefly show the
	// queue as it was BEFORE the user's action — a visible flicker back and
	// forth. The optimistic local update (noteEnqueued/noteQueueReplaced)
	// shows the result instantly, and the poll skip keeps it on screen until
	// endQueueMutation reconciles with the settled server state.
	pending int

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

// be returns the live playback backend. It is read through the session (not
// cached) so a backend swap from the settings page — which updates
// a.queue.session — reaches every queue operation without re-wiring.
func (q *queuePage) be() playback.PlaybackBackend {
	return q.session.Backend
}

// newQueuePage builds the queue view once; show() (re)binds it to the live poll.
func newQueuePage(win fyne.Window, session *data.Session, transport *player, covers *coverStore, compact bool, back func(), onColumnsChanged func([]string)) *queuePage {
	q := &queuePage{
		win:       win,
		session:   session,
		transport: transport,
		covers:    covers,
		compact:   compact,
		back:      back,
		current:   -1,
	}

	q.table = newTrackTable(win, nil, session.Cfg.QueueColumns,
		nil, // single-tap selects (multiSelect); double-tap plays via onDoubleTap
		onColumnsChanged,
		trackTableOpts{
			indicator:   q.rowIndicator,
			onReorder:   q.onReorder,
			onDragStart: q.onDragStart,
			onDragMove:  q.onDragMove,
			onDoubleTap: q.playDisplayRow,
			multiSelect: true,
			covers:      covers,
			// The queue groups consecutive same-album tracks under a header row
			// carrying the cover, artist and album name, so there is no
			// per-track Art column (defaultQueueColumns is also the available
			// set: the Columns dialog cannot bring it back).
			grouped:       true,
			defaultCols:   defaultQueueColumns,
			availableCols: defaultQueueColumns,
		},
	)
	q.list = newQueueList(q)

	q.toolbarHolder = container.NewStack()
	q.bodyHolder = container.NewStack()
	q.object = container.NewStack(container.NewBorder(q.toolbarHolder, nil, nil, nil, q.bodyHolder))
	q.applyLayout()
	return q
}

// applyLayout installs the toolbar and body for the current layout mode, plus
// the compact layout's back-swipe strip.
func (q *queuePage) applyLayout() {
	q.toolbarHolder.Objects = []fyne.CanvasObject{q.buildToolbar()}
	q.toolbarHolder.Refresh()

	if q.compact {
		q.bodyHolder.Objects = []fyne.CanvasObject{q.list.Object()}
	} else {
		q.bodyHolder.Objects = []fyne.CanvasObject{q.table.object}
	}
	q.bodyHolder.Refresh()

	// A left-edge rightward swipe returns to the cover wall (mirrors the
	// gallery's left→right swipe); the strip sits above the content but only
	// occupies the left edge, leaving the list free to scroll. It exists only
	// in the compact layout, where the queue is a full-screen view.
	base := q.object.Objects[0]
	if q.compact {
		strip := newEdgeSwipe(q.leave)
		q.object.Objects = []fyne.CanvasObject{base, container.NewBorder(nil, nil, strip, nil, nil)}
	} else {
		q.object.Objects = []fyne.CanvasObject{base}
	}
	q.object.Refresh()
}

// setCompact switches the rendering when the window crosses the compact width
// threshold. The row model is shared, so the new view is populated immediately.
func (q *queuePage) setCompact(compact bool) {
	if q.compact == compact {
		return
	}
	q.compact = compact
	q.applyLayout()
	q.rebuildTracks()
}

// Object returns the page's root object for the shell to place.
func (q *queuePage) Object() fyne.CanvasObject { return q.object }

// rowIndicator returns the play glyph for the current row, else "". A text glyph
// (not a widget.Icon) because Icon.SetResource(nil) does not reliably repaint
// inside a recycled table cell — the old play icon lingers on rows the current
// track has moved past. Labels repaint correctly here. The row argument is a
// display row; the table's grouped row model maps it to a playlist index.
func (q *queuePage) rowIndicator(row int) string {
	if q.playlistIndexForRow(row) == q.current && q.current >= 0 {
		return "▶" // ▶
	}
	return ""
}

// playlistIndexForRow maps a display row in the grouped table to its playlist
// index, or -1 for album header rows and out-of-range indices.
func (q *queuePage) playlistIndexForRow(row int) int {
	if row < 0 || row >= len(q.table.rows) {
		return -1
	}
	return q.table.rows[row].trackIndex
}

// playlistIndexForRowLenient maps a display row to a playlist index like
// playlistIndexForRow, but an album header row maps to the index of its first
// track (dropping a row onto a header means "before that album").
func (q *queuePage) playlistIndexForRowLenient(row int) int {
	if row < 0 || row >= len(q.table.rows) {
		return -1
	}
	r := q.table.rows[row]
	if r.kind == queueRowAlbum {
		return r.groupStart
	}
	return r.trackIndex
}

// playlistGapAt maps a display-row insertion gap (rows above the gap, in
// [0, len(rows)]) to a playlist gap: the playlist index of the first track at
// or below the gap, i.e. the count of tracks above it.
func (q *queuePage) playlistGapAt(displayGap int) int {
	for i := displayGap; i < len(q.table.rows); i++ {
		if q.table.rows[i].kind == queueRowTrack {
			return q.table.rows[i].trackIndex
		}
	}
	return len(q.playlist)
}

// buildToolbar builds the top row: shuffle, repeat, save, clear, columns. The
// compact layout drops the labels (and the Columns button, which configures a
// table it does not show) and gains a back button, since the queue is a
// full-screen view there.
func (q *queuePage) buildToolbar() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Playlist", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	if q.compact {
		back := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), q.leave)
		back.Importance = widget.LowImportance
		shuffle := widget.NewButtonWithIcon("", theme.MediaReplayIcon(), q.shuffle)
		save := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), q.saveQueue)
		clear := widget.NewButtonWithIcon("", theme.DeleteIcon(), q.clearPlaylist)
		q.repeatBtn = widget.NewButton("", q.toggleRepeat)
		q.refreshRepeatLabel()
		row := container.NewBorder(nil, nil, back, container.NewHBox(shuffle, q.repeatBtn, save, clear), title)
		return container.NewVBox(row, widget.NewSeparator())
	}

	shuffle := widget.NewButtonWithIcon("Shuffle", theme.MediaReplayIcon(), q.shuffle)
	save := widget.NewButtonWithIcon("Save as playlist", theme.DocumentSaveIcon(), q.saveQueue)
	clear := widget.NewButtonWithIcon("Clear", theme.DeleteIcon(), q.clearPlaylist)
	columns := widget.NewButtonWithIcon("Columns", theme.MenuIcon(), q.table.showColumnsDialog)
	q.repeatBtn = widget.NewButton("", q.toggleRepeat)
	q.refreshRepeatLabel()

	buttons := container.NewHBox(shuffle, q.repeatBtn, save, clear, columns)
	return container.NewVBox(buttons, title, widget.NewSeparator())
}

// show binds the page to the transport's status poll and triggers an immediate
// refresh so the view is populated without waiting a poll interval.
func (q *queuePage) show() {
	q.table.show() // size columns now that the canvas width is known
	q.transport.SetStatusListener(q.applyStatus)
	go func() {
		s, err := q.be().Status()
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

// noteEnqueued reflects a just-issued enqueue locally: the tracks appear in
// the table immediately instead of whenever the server's asynchronous add
// lands. The matching endQueueMutation lifts the poll skip and reconciles.
func (q *queuePage) noteEnqueued(urls []string) {
	q.pending++
	q.playlist = append(q.playlist, urls...)
	q.rebuildTracks()
}

// noteQueueReplaced reflects a just-issued queue replacement (Play album)
// locally, so the table shows the new queue instantly rather than after the
// server's append-trim-settle dance (which can take a second over a large
// old queue). The matching endQueueMutation lifts the poll skip and
// reconciles.
func (q *queuePage) noteQueueReplaced(urls []string) {
	q.pending++
	q.playlist = append(q.playlist[:0], urls...)
	q.current = -1 // playback of the new queue has not been observed yet
	q.rebuildTracks()
}

// endQueueMutation marks a mutation started via noteEnqueued /
// noteQueueReplaced as settled (the backend call blocks until pwplay has
// applied it), lifting the poll skip and forcing one status refresh so the
// table converges to the server's state immediately.
func (q *queuePage) endQueueMutation() {
	go func() {
		s, err := q.be().Status()
		fyne.Do(func() {
			if q.pending > 0 {
				q.pending--
			}
			if err == nil {
				q.applyStatus(s)
			}
		})
	}()
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
// mid-drag so a rebuild does not disrupt the gesture, and while a user-started
// queue mutation is in flight so a stale mid-mutation server state cannot
// flicker the table back to before the action.
func (q *queuePage) applyStatus(s playback.Status) {
	if q.dragging || q.pending > 0 {
		return
	}
	q.playlist = append(q.playlist[:0], s.Playlist...)
	q.current = s.CurrentTrack
	q.rebuildTracks()
}

// rebuildTracks resolves each playlist URL to its registered track metadata,
// falling back to a stub whose Display() is the URL's last segment, then pushes
// the slice into both renderings (the table refreshes itself; the list rebuilds
// its album grouping). Feeding both, regardless of which is on screen, is what
// lets a layout-mode switch show a populated view immediately.
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
	q.list.setTracks(q.qtracks, q.current)
}

// queueLen is the number of entries in the queue, for drag clamping.
func (q *queuePage) queueLen() int { return len(q.playlist) }

// setDragging marks a gesture in progress so status polls don't rebuild the
// rows mid-drag (which would pull the row out from under the finger).
func (q *queuePage) setDragging(on bool) { q.dragging = on }

// moveTrack relocates one queue entry, updating the rows immediately and
// committing to the backend; the next poll reconciles any drift.
func (q *queuePage) moveTrack(from, to int) {
	n := len(q.playlist)
	if from < 0 || to < 0 || from >= n || to >= n || from == to {
		q.rebuildTracks()
		return
	}
	moveOne(q.playlist, from, to)
	q.current = shiftIndexOnMove(q.current, from, to)
	q.rebuildTracks()
	q.commit([][2]int{{from, to}})
}

// moveBlock relocates the run [start, start+count) so its top lands at the
// insertion gap `gap`, used by the compact list's album-level move actions.
func (q *queuePage) moveBlock(start, count, gap int) {
	n := len(q.playlist)
	if count <= 0 || start < 0 || start+count > n {
		return
	}
	sel := make([]int, 0, count)
	for i := start; i < start+count; i++ {
		sel = append(sel, i)
	}
	want := reorderSelection(n, sel, gap)
	if want == nil {
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
	q.rebuildTracks()
	q.commit(moves)
}

// removeRange drops count entries starting at index from the queue. Removals go
// back to front because each backend removal reindexes the entries after it.
func (q *queuePage) removeRange(start, count int) {
	n := len(q.playlist)
	if count <= 0 || start < 0 || start+count > n {
		return
	}
	// Optimistic local update, so the row disappears on touch-release rather
	// than at the next poll.
	q.playlist = append(q.playlist[:start], q.playlist[start+count:]...)
	switch {
	case q.current >= start+count:
		q.current -= count
	case q.current >= start:
		q.current = -1 // the playing entry itself went away
	}
	q.rebuildTracks()

	go func() {
		for i := start + count - 1; i >= start; i-- {
			if err := q.be().Remove(i); err != nil {
				fyne.Do(func() { dialog.ShowError(err, q.win) })
				return
			}
		}
		if s, err := q.be().Status(); err == nil {
			fyne.Do(func() { q.applyStatus(s) })
		}
	}()
}

// showTrackRowMenu offers the per-track actions of the compact playlist, where
// there is no room for a multi-select table and its drag semantics.
func (q *queuePage) showTrackRowMenu(row queueRow, pos fyne.Position) {
	idx := row.trackIndex
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Play", func() { q.playRow(idx) }),
		fyne.NewMenuItem("Remove track", func() { q.removeRange(idx, 1) }),
	}
	if idx > 0 {
		items = append(items, fyne.NewMenuItem("Move up", func() { q.moveTrack(idx, idx-1) }))
	}
	if idx < len(q.playlist)-1 {
		items = append(items, fyne.NewMenuItem("Move down", func() { q.moveTrack(idx, idx+1) }))
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), q.win.Canvas(), pos)
}

// showAlbumRowMenu offers the album-level actions of the compact playlist: the
// whole run of consecutive entries the header covers is played, removed or
// moved as one block.
func (q *queuePage) showAlbumRowMenu(row queueRow, pos fyne.Position) {
	start, count := row.groupStart, row.groupCount
	items := []*fyne.MenuItem{
		fyne.NewMenuItem("Play album", func() { q.playRow(start) }),
		fyne.NewMenuItem("Remove album", func() { q.removeRange(start, count) }),
	}
	if start > 0 {
		items = append(items, fyne.NewMenuItem("Move album up", func() { q.moveBlock(start, count, start-1) }))
	}
	if start+count < len(q.playlist) {
		items = append(items, fyne.NewMenuItem("Move album down", func() { q.moveBlock(start, count, start+count+1) }))
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), q.win.Canvas(), pos)
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
		text := q.trackLabel(q.playlistIndexForRow(q.dragFrom))
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
// pos and returns the playlist gap it points to. The table reports a display-row
// gap; playlistGapAt maps it through the grouped row model (the album headers
// occupy display rows but no playlist slots).
func (q *queuePage) dropLineAt(pos fyne.Position) int {
	return q.playlistGapAt(q.table.showInsertionLineAt(pos))
}

// gapAt returns the playlist gap under pos without drawing anything.
func (q *queuePage) gapAt(pos fyne.Position) int {
	return q.playlistGapAt(q.table.gapAt(pos))
}

// clearDropLine hides the external-drag insertion indicator.
func (q *queuePage) clearDropLine() { q.table.hideInsertionLine() }

// insertTracksAt registers the tracks' metadata and inserts their stream URLs at
// playlist position gap, then forces an immediate refresh: the periodic poll
// has an up-to-500ms lag, which made a dropped album appear to not land in
// the list.
func (q *queuePage) insertTracksAt(gap int, urls []string, meta []data.Track) {
	if len(urls) == 0 {
		return
	}
	q.transport.AppendQueue(urls, meta)
	go func() {
		if err := q.be().Insert(gap, urls...); err != nil {
			fyne.Do(func() { dialog.ShowError(err, q.win) })
			return
		}
		// Insert blocks until the backend has applied the add (and the move),
		// so the status fetched here already lists the tracks; update the
		// table now rather than at the next poll tick.
		if s, err := q.be().Status(); err == nil {
			fyne.Do(func() { q.applyStatus(s) })
		}
	}()
}

// onReorder moves the dragged rows locally for instant feedback, then commits
// the change to the backend; the next poll reconciles any drift. The row
// arguments are display rows in the grouped table; they are mapped to
// playlist indices first (album header rows cannot be grabbed — the table
// marks them non-selectable). A single grabbed row is one MoveItems; a
// multi-row selection is diffed into a sequence of single-item moves that
// reproduce the new order.
func (q *queuePage) onReorder(from, to int) {
	q.dragging = false
	q.clearDragGhost()
	sel := q.dragRows
	q.dragRows = nil
	n := len(q.playlist)

	fromP := q.playlistIndexForRow(from)
	toP := q.playlistIndexForRowLenient(to)
	if fromP < 0 || toP < 0 || fromP >= n || toP >= n || from == to {
		q.rebuildTracks()
		return
	}

	if len(sel) <= 1 {
		q.moveTrack(fromP, toP)
		return
	}

	// Map the display selection to playlist indices.
	selP := make([]int, 0, len(sel))
	for _, r := range sel {
		if idx := q.playlistIndexForRow(r); idx >= 0 {
			selP = append(selP, idx)
		}
	}
	// Recover the insertion gap the drop landed at from the grabbed row's final
	// index (the inverse of the widget's single-row gap→index conversion), so the
	// moved block's top aligns with the drawn drop line, then map it to a
	// playlist gap through the grouped row model.
	gap := to
	if to > from {
		gap = to + 1
	}
	want := reorderSelection(n, selP, q.playlistGapAt(gap))
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

// playDisplayRow plays the track behind a double-tapped table row, mapping
// the display row to its playlist index. Album header rows (trackIndex -1)
// are inert.
func (q *queuePage) playDisplayRow(displayRow int) {
	if idx := q.playlistIndexForRow(displayRow); idx >= 0 {
		q.playRow(idx)
	}
}

// playRow jumps playback to the double-tapped queue position and makes sure
// playback actually starts: pwplay's Goto loads the target track even while
// paused or stopped, but it only clears the stopped flag — a paused player
// stays paused. A double-click means "play this track", so once the jump has
// landed (bounded wait for the decoder loop to apply it) a Play is issued if
// the server still isn't playing.
func (q *queuePage) playRow(row int) {
	if row < 0 || row >= len(q.playlist) {
		return
	}
	go func() {
		if err := q.be().Goto(row); err != nil {
			fyne.Do(func() { dialog.ShowError(err, q.win) })
			return
		}
		for i := 0; i < 60; i++ {
			time.Sleep(50 * time.Millisecond)
			s, err := q.be().Status()
			if err != nil {
				return
			}
			if s.CurrentTrack != row {
				continue // the jump has not been applied yet
			}
			if !s.Playing {
				if err := q.be().Play(); err != nil {
					fyne.Do(func() { dialog.ShowError(err, q.win) })
				}
			}
			return
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
			if err := q.be().MoveItems(m[0], 1, m[1]); err != nil {
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
			if err := q.be().MoveItems(m[0], 1, m[1]); err != nil {
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

// clearPlaylist empties the queue after a confirmation prompt: the table
// clears immediately (the backend applies the clear in one step), then a
// refresh reflects the now-empty, stopped state.
func (q *queuePage) clearPlaylist() {
	if len(q.playlist) == 0 {
		return
	}
	dialog.ShowConfirm("Clear playlist", "Remove all tracks from the playlist?", func(ok bool) {
		if !ok {
			return
		}
		q.playlist = q.playlist[:0]
		q.rebuildTracks()
		go func() {
			if err := q.be().Clear(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, q.win) })
				return
			}
			if s, err := q.be().Status(); err == nil {
				fyne.Do(func() { q.applyStatus(s) })
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
