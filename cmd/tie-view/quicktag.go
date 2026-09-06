package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sort"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/uidbz/tie/client"
)

// quickTagIconSize returns the default quick tag button edge length.
func quickTagIconSize(mobile bool) float32 {
	if mobile {
		return 56
	}
	return 40
}

// quickTagBar is the quick tagging mode's control strip: a translucent pill of
// icon buttons, one per configured tag, plus a 1-5 star rating control, laid
// over the top or bottom edge of the single-image view so the picture stays
// fully visible. Tapping a button (or pressing its key) toggles that tag on
// the displayed image and writes the change to tie immediately; the icon flips
// optimistically and reverts if the write fails. The stars set the image's
// rating the same way. A one-line status above/below the pill names the
// hovered control (desktop) and confirms changes.
//
// The applied-tag set and rating are seeded from the tieReader's expanded
// query attributes so the bar paints correctly the instant an image opens,
// then reconciled against a background tc.Get so directory-browse images
// (whose listings carry no tags) and out-of-band edits are shown correctly
// too.
//
// All fields are UI-goroutine state; network calls run in goroutines and
// come back through fyne.Do.
type quickTagBar struct {
	widget.BaseWidget
	tc      *client.TieClient
	baseDir string // icon paths resolve against this
	mobile  bool

	cfg      QuickTagSet // normalized; the active collection's set
	iconSize float32
	cells    []*quickTagCell
	stars    []*starRating // one per pill rendering (row and stacked); all painted alike
	status   *widget.Label
	statusBg *canvas.Rectangle
	// statusBox centers the status label over its backdrop; it is refreshed
	// on every text change so the backdrop re-fits the new text width.
	statusBox *fyne.Container
	root      *fyne.Container // renderer content; rebuilt by Rebuild
	// Overlay is the full-size container that anchors the bar to the image
	// edge selected by cfg.Position (and, with Rating on the opposite edge,
	// the rating strip to that edge). Append it to viewer.Content.Objects.
	Overlay *fyne.Container

	hash          string          // content hash of the displayed image ("" = none)
	reader        *tieReader      // its reader, whose tag/rating cache is kept in sync
	applied       map[string]bool // every tag on the image, not just quick ones
	rating        int             // 0 = unrated, else 1..5
	pending       map[string]bool // tag toggles made since the reconcile fetch started
	ratingPending *int            // rating set since the reconcile fetch started
	gen           int             // bumped per SetImage; stale fetches compare it
	flashID       int             // bumped per status flash; stale timers compare it
	known         map[string]bool // tags registered in ("tags","all") this session

	// OnTagsChanged, when non-nil, is called on the UI goroutine after a toggle
	// with the image's full tag list, so the image tagger panel can follow.
	OnTagsChanged func(hash string, tags []string)
	// OnRatingChanged, when non-nil, is called on the UI goroutine after the
	// user rates the image here, so the image tagger panel can follow.
	OnRatingChanged func(hash string, rating int)
}

// newQuickTagBar builds the bar for cfg. baseDir resolves relative icon paths.
func newQuickTagBar(tc *client.TieClient, cfg QuickTagSet, baseDir string, mobile bool) *quickTagBar {
	b := &quickTagBar{
		tc:      tc,
		baseDir: baseDir,
		mobile:  mobile,
		applied: map[string]bool{},
		pending: map[string]bool{},
		known:   map[string]bool{},
	}
	b.status = widget.NewLabel("")
	b.status.Alignment = fyne.TextAlignCenter
	b.status.TextStyle = fyne.TextStyle{Bold: true}
	b.statusBg = canvas.NewRectangle(color.Transparent)
	b.statusBg.CornerRadius = 6
	b.statusBox = container.NewCenter(container.NewStack(b.statusBg, b.status))
	b.root = container.NewStack()
	b.Overlay = container.NewWithoutLayout()
	b.ExtendBaseWidget(b)
	b.Rebuild(cfg)
	return b
}

// CreateRenderer implements fyne.Widget.
func (b *quickTagBar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.root)
}

// pill wraps content in the translucent rounded backdrop; the tapSink keeps
// near-miss taps from falling through to the image.
func (b *quickTagBar) pill(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(color.NRGBA{A: 150})
	bg.CornerRadius = 10
	return container.NewCenter(newTapSink(container.NewStack(bg, container.NewPadded(content))))
}

// Rebuild replaces the bar's controls from cfg (after the settings editor
// applies a change or the active collection switches) and re-anchors Overlay
// to the configured edges. The current image and its applied state are kept.
func (b *quickTagBar) Rebuild(cfg QuickTagSet) {
	b.cfg = cfg.normalized()
	b.iconSize = b.cfg.IconSize
	if b.iconSize <= 0 {
		b.iconSize = quickTagIconSize(b.mobile)
	}

	// Tag buttons.
	b.cells = b.cells[:0]
	cellObjs := make([]fyne.CanvasObject, 0, len(b.cfg.Tags)+2)
	for _, e := range b.cfg.Tags {
		c := newQuickTagCell(b, e, resolveQuickTagIcon(b.baseDir, e.On), resolveQuickTagIcon(b.baseDir, e.Off))
		b.cells = append(b.cells, c)
		cellObjs = append(cellObjs, c)
	}

	// Rating stars: slightly smaller than the icons so five of them plus a
	// few buttons still fit a phone width when inline. Each pill rendering
	// gets its own widget (a canvas object has one parent); paint syncs them.
	b.stars = b.stars[:0]
	newStars := func() fyne.CanvasObject {
		sr := newStarRating(b.iconSize*0.75, b.rate)
		sr.OnHover = func(n int) {
			if n == 0 {
				b.setStatus("")
			} else {
				b.setStatus(fmt.Sprintf("rating %d", n))
			}
		}
		b.stars = append(b.stars, sr)
		return container.NewCenter(sr)
	}
	var starsObj fyne.CanvasObject
	if b.cfg.Rating != ratingOff {
		starsObj = newStars()
	}

	// Pills. Inline rating gets two renderings — one row [stars | tags] and
	// the two stacked pills — and barLayout shows whichever fits the width
	// (a phone in portrait rarely fits five stars plus several buttons).
	var starsPill, tagsPill, rowPill fyne.CanvasObject
	if starsObj != nil {
		starsPill = b.pill(starsObj)
	}
	if len(cellObjs) > 0 {
		tagsPill = b.pill(container.NewHBox(cellObjs...))
	} else if starsObj == nil {
		tagsPill = b.pill(widget.NewLabel("No quick tags configured — see Settings → Quick tags"))
	}
	if b.cfg.Rating == ratingInline && starsObj != nil && len(cellObjs) > 0 {
		row := append([]fyne.CanvasObject{newStars(), widget.NewSeparator()}, cellObjs...)
		rowPill = b.pill(container.NewHBox(row...))
	}

	// The column at the tags' edge: status line nearest the image, then the
	// pills (stars nearer the image than the tags), tags at the very edge.
	// A rating strip on the opposite edge is anchored there instead.
	top := b.cfg.Position == "top"
	var farStrip fyne.CanvasObject
	lay := &barLayout{bar: b, top: top, status: b.statusBox, row: rowPill, tags: tagsPill}
	switch b.cfg.Rating {
	case ratingInline:
		lay.stars = starsPill
	case ratingTop, ratingBottom:
		if b.cfg.Rating == b.cfg.Position {
			lay.stars = starsPill
		} else {
			farStrip = starsPill
		}
	}
	// Without a merged row there is nothing to switch between: always show
	// the separate pills (stars alone, tags alone, or both stacked).
	lay.stacked = rowPill == nil
	var colObjs []fyne.CanvasObject
	for _, o := range []fyne.CanvasObject{b.statusBox, rowPill, starsPill, tagsPill} {
		if o != nil && o != farStrip {
			colObjs = append(colObjs, o)
		}
	}
	lay.apply()
	b.root.Objects = []fyne.CanvasObject{container.New(lay, colObjs...)}

	// Anchor: the bar widget at its edge, the far rating strip opposite.
	objs := []fyne.CanvasObject{b}
	var edgeTop, edgeBottom fyne.CanvasObject
	if top {
		edgeTop = b
	} else {
		edgeBottom = b
	}
	if farStrip != nil {
		objs = append(objs, farStrip)
		if top {
			edgeBottom = farStrip
		} else {
			edgeTop = farStrip
		}
	}
	b.Overlay.Layout = layout.NewBorderLayout(edgeTop, edgeBottom, nil, nil)
	b.Overlay.Objects = objs

	b.paint()
	b.root.Refresh()
	b.Refresh()
	b.Overlay.Refresh()
}

// barLayout stacks the bar's status line and pills vertically, ordered so the
// status is nearest the image and the tags at the screen edge. When both a
// single-row pill (row) and the split pills (stars, tags) exist, Layout picks
// the row while it fits the available width and the stacked pair otherwise;
// a mode change reschedules the parent's layout so the bar's height follows.
type barLayout struct {
	bar         *quickTagBar
	top         bool
	status, row fyne.CanvasObject
	stars, tags fyne.CanvasObject
	stacked     bool
}

// rows returns the visible objects in image→edge order.
func (l *barLayout) rows() []fyne.CanvasObject {
	var pills []fyne.CanvasObject
	if l.stacked {
		if l.stars != nil {
			pills = append(pills, l.stars)
		}
		if l.tags != nil {
			pills = append(pills, l.tags)
		}
	} else if l.row != nil {
		pills = append(pills, l.row)
	}
	return append([]fyne.CanvasObject{l.status}, pills...)
}

// apply shows the objects of the current mode and hides the others.
func (l *barLayout) apply() {
	for _, o := range []fyne.CanvasObject{l.row, l.stars, l.tags} {
		if o == nil {
			continue
		}
		if o == l.row {
			if l.stacked {
				o.Hide()
			} else {
				o.Show()
			}
		} else if l.stacked {
			o.Show()
		} else {
			o.Hide()
		}
	}
}

func (l *barLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for i, o := range l.rows() {
		m := o.MinSize()
		if m.Width > w {
			w = m.Width
		}
		if i > 0 {
			h += theme.Padding()
		}
		h += m.Height
	}
	return fyne.NewSize(w, h)
}

func (l *barLayout) Layout(_ []fyne.CanvasObject, size fyne.Size) {
	if l.row != nil && l.stars != nil && l.tags != nil {
		stacked := l.row.MinSize().Width > size.Width
		if stacked != l.stacked {
			l.stacked = stacked
			l.apply()
			// Our MinSize changed; let the Border layout re-run with it.
			overlay := l.bar.Overlay
			fyne.Do(overlay.Refresh)
		}
	}
	rows := l.rows()
	if l.top {
		// Edge first: tags at the top, status nearest the image.
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	var y float32
	for _, o := range rows {
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h + theme.Padding()
	}
}

// Keys returns the bar's shortcut bindings: pressing an entry's key toggles
// its tag, pressing a RatingKeys entry sets (or clears) that star count.
// Register them on each ImageView (desktop TypedKey) and dispatch them from
// the window-level handler (mobile).
func (b *quickTagBar) Keys() map[fyne.KeyName]func() {
	keys := make(map[fyne.KeyName]func(), len(b.cfg.Tags)+len(b.cfg.RatingKeys))
	add := func(k fyne.KeyName, fn func()) {
		if prev, dup := keys[k]; dup {
			keys[k] = func() { prev(); fn() }
			return
		}
		keys[k] = fn
	}
	for _, e := range b.cfg.Tags {
		if e.Key == "" {
			continue
		}
		tag := e.Tag
		add(fyne.KeyName(e.Key), func() { b.toggle(tag) })
	}
	if b.cfg.Rating != ratingOff {
		for i, k := range b.cfg.RatingKeys {
			if k == "" {
				continue
			}
			n := i + 1
			add(fyne.KeyName(k), func() {
				if b.rating == n {
					b.rate(0)
				} else {
					b.rate(n)
				}
			})
		}
	}
	return keys
}

// SetImage retargets the bar to the image behind r (nil clears it). The
// controls paint from r's cached tags and rating at once; a background
// tc.Get then reconciles them and refreshes r's cache.
func (b *quickTagBar) SetImage(r *tieReader) {
	b.gen++
	b.pending = map[string]bool{}
	b.ratingPending = nil
	b.setStatus("")
	if r == nil {
		b.hash, b.reader = "", nil
		b.setApplied(nil)
		b.rating = 0
		b.paint()
		return
	}
	b.hash, b.reader = r.hash, r
	b.setApplied(r.tags)
	b.rating = r.rating
	b.paint()

	gen, hash := b.gen, r.hash
	go func() {
		row, err := b.tc.Get(hash)
		if err != nil {
			fmt.Println("quicktag: error fetching tags:", err)
			return
		}
		tags := client.RowValues(row, "tag")
		rating := rowRating(row)
		fyne.Do(func() {
			if b.gen != gen {
				return // stale: another image is showing
			}
			b.setApplied(tags)
			b.rating = rating
			// Keep changes the user made while the fetch was in flight.
			for tag, on := range b.pending {
				b.setTag(tag, on)
			}
			if b.ratingPending != nil {
				b.rating = *b.ratingPending
			}
			b.syncReader()
			b.paint()
		})
	}()
}

// SetTags replaces the applied set for hash from an external source (the
// image tagger panel) without writing to tie. Ignored for other images.
func (b *quickTagBar) SetTags(hash string, tags []string) {
	if hash == "" || hash != b.hash {
		return
	}
	b.setApplied(tags)
	b.syncReader()
	b.paint()
}

// SetRating replaces the rating for hash from an external source (the image
// tagger panel) without writing to tie. Ignored for other images.
func (b *quickTagBar) SetRating(hash string, rating int) {
	if hash == "" || hash != b.hash {
		return
	}
	b.rating = rating
	b.syncReader()
	b.paint()
}

// Applied reports whether tag is currently applied to the displayed image.
func (b *quickTagBar) Applied(tag string) bool { return b.applied[tag] }

// Rating returns the displayed image's rating (0 = unrated).
func (b *quickTagBar) Rating() int { return b.rating }

// setApplied replaces the applied set.
func (b *quickTagBar) setApplied(tags []string) {
	b.applied = make(map[string]bool, len(tags))
	for _, t := range tags {
		b.applied[t] = true
	}
}

// setTag sets one tag's membership in the applied set.
func (b *quickTagBar) setTag(tag string, on bool) {
	if on {
		b.applied[tag] = true
	} else {
		delete(b.applied, tag)
	}
}

// appliedList returns the applied set as a sorted slice.
func (b *quickTagBar) appliedList() []string {
	tags := make([]string, 0, len(b.applied))
	for t := range b.applied {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// syncReader mirrors the applied set and rating into the reader's cache so a
// later SetImage for the same entry paints without waiting for the network.
func (b *quickTagBar) syncReader() {
	if b.reader != nil && b.reader.hash == b.hash {
		b.reader.setTags(b.appliedList())
		b.reader.setRating(b.rating)
	}
}

// paint pushes the applied set and rating into every control.
func (b *quickTagBar) paint() {
	for _, c := range b.cells {
		c.setApplied(b.applied[c.entry.Tag])
	}
	for _, sr := range b.stars {
		sr.SetRating(b.rating)
	}
}

// toggle flips tag on the displayed image: optimistic UI update, then the
// tie write in a goroutine, reverting on failure. Adds also register the tag
// in the ("tags","all") index (once per session) so it reaches the sidebar.
func (b *quickTagBar) toggle(tag string) {
	if b.hash == "" || tag == "" {
		return
	}
	hash := b.hash
	on := !b.applied[tag]
	b.setTag(tag, on)
	b.pending[tag] = on
	b.paint()
	b.syncReader()
	if on {
		b.flash("+ " + tag)
	} else {
		b.flash("− " + tag)
	}
	if b.OnTagsChanged != nil {
		b.OnTagsChanged(hash, b.appliedList())
	}
	register := on && !b.known[tag]
	b.known[tag] = true

	go func() {
		var err error
		if on {
			_, err = b.tc.Add(hash, "tag", tag)
			if err == nil && register {
				if _, rerr := b.tc.Add(client.TieTags.String(), client.TieAll.String(), tag); rerr != nil {
					fmt.Printf("quicktag: error registering tag %q: %v\n", tag, rerr)
				}
			}
		} else {
			_, err = b.tc.Delete(hash, "tag", tag)
		}
		if err == nil {
			return
		}
		fmt.Printf("quicktag: error writing tag %q: %v\n", tag, err)
		fyne.Do(func() {
			if register {
				b.known[tag] = false
			}
			if b.hash != hash {
				return // user moved on; the reconcile fetch will show the truth
			}
			b.setTag(tag, !on)
			delete(b.pending, tag)
			b.paint()
			b.syncReader()
			b.flash("failed: " + tag)
			if b.OnTagsChanged != nil {
				b.OnTagsChanged(hash, b.appliedList())
			}
		})
	}()
}

// rate sets the displayed image's rating (0 clears): optimistic UI update,
// then the tie write (delete old, add new) in a goroutine, reverting on
// failure. Called by the star widget on taps and by the RatingKeys bindings.
func (b *quickTagBar) rate(rating int) {
	if b.hash == "" {
		b.paint() // undo the tapped widget's own paint
		return
	}
	hash := b.hash
	old := b.rating
	if rating == old {
		return
	}
	b.rating = rating
	r := rating
	b.ratingPending = &r
	b.paint()
	b.syncReader()
	if rating == 0 {
		b.flash("rating cleared")
	} else {
		b.flash(fmt.Sprintf("rating %d", rating))
	}
	if b.OnRatingChanged != nil {
		b.OnRatingChanged(hash, rating)
	}

	go func() {
		var err error
		if old != 0 {
			_, err = b.tc.Delete(hash, "rating", strconv.Itoa(old))
		}
		if err == nil && rating != 0 {
			_, err = b.tc.Add(hash, "rating", strconv.Itoa(rating))
		}
		if err == nil {
			return
		}
		fmt.Printf("quicktag: error writing rating %d: %v\n", rating, err)
		fyne.Do(func() {
			if b.hash != hash {
				return
			}
			b.rating = old
			b.ratingPending = nil
			b.paint()
			b.syncReader()
			b.flash("failed: rating")
			if b.OnRatingChanged != nil {
				b.OnRatingChanged(hash, old)
			}
		})
	}()
}

// setStatus shows text in the status line (empty hides its backdrop).
func (b *quickTagBar) setStatus(text string) {
	b.flashID++
	b.status.SetText(text)
	if text == "" {
		b.statusBg.FillColor = color.Transparent
	} else {
		b.statusBg.FillColor = color.NRGBA{A: 150}
	}
	b.statusBg.Refresh()
	b.statusBox.Refresh()
}

// flash shows text in the status line for a moment, then clears it unless a
// newer message has replaced it.
func (b *quickTagBar) flash(text string) {
	b.setStatus(text)
	id := b.flashID
	time.AfterFunc(1200*time.Millisecond, func() {
		fyne.Do(func() {
			if b.flashID == id {
				b.setStatus("")
			}
		})
	})
}

// quickTagCell is one button on the bar. With two icons it swaps them; with
// only an On icon it shows a grayscale copy while the tag is not applied;
// with none it shows the tag name, highlighted while applied.
type quickTagCell struct {
	widget.BaseWidget
	bar     *quickTagBar
	entry   quickTagEntry
	on, off fyne.Resource
	img     *canvas.Image
	text    *canvas.Text
	bg      *canvas.Rectangle
	applied bool
}

func newQuickTagCell(bar *quickTagBar, e quickTagEntry, on, off fyne.Resource) *quickTagCell {
	c := &quickTagCell{bar: bar, entry: e, on: on, off: off}
	if c.on == nil && c.off != nil {
		// Only an Off icon: treat it as the single icon.
		c.on, c.off = c.off, nil
	}
	if c.on != nil && c.off == nil {
		c.off = grayscaleResource(c.on)
	}
	c.bg = canvas.NewRectangle(color.Transparent)
	c.bg.CornerRadius = 6
	if c.on != nil {
		c.img = canvas.NewImageFromResource(c.on)
		c.img.FillMode = canvas.ImageFillContain
		c.img.ScaleMode = canvas.ImageScaleSmooth
	} else {
		c.text = canvas.NewText(e.Tag, theme.Color(theme.ColorNameForeground))
		c.text.TextStyle = fyne.TextStyle{Bold: true}
		c.text.TextSize = theme.TextSize()
	}
	c.ExtendBaseWidget(c)
	c.setApplied(false)
	return c
}

// CreateRenderer implements fyne.Widget.
func (c *quickTagCell) CreateRenderer() fyne.WidgetRenderer {
	var content fyne.CanvasObject
	if c.img != nil {
		content = container.NewPadded(c.img)
	} else {
		content = container.NewCenter(c.text)
	}
	return widget.NewSimpleRenderer(container.NewStack(c.bg, content))
}

// MinSize is a square of the bar's icon size, widened for long text labels.
func (c *quickTagCell) MinSize() fyne.Size {
	s := c.bar.iconSize
	if c.text != nil {
		w := c.text.MinSize().Width + 2*theme.Padding()
		if w > s {
			return fyne.NewSize(w, s)
		}
	}
	return fyne.NewSize(s, s)
}

// setApplied repaints the cell for the given state.
func (c *quickTagCell) setApplied(applied bool) {
	c.applied = applied
	switch {
	case c.img != nil && c.off != nil:
		if applied {
			c.img.Resource = c.on
		} else {
			c.img.Resource = c.off
		}
		c.img.Translucency = 0
		c.img.Refresh()
	case c.img != nil:
		// Grayscale conversion failed (undecodable icon): dim instead.
		c.img.Resource = c.on
		if applied {
			c.img.Translucency = 0
		} else {
			c.img.Translucency = 0.7
		}
		c.img.Refresh()
	default:
		if applied {
			c.text.Color = theme.Color(theme.ColorNameForegroundOnPrimary)
			c.bg.FillColor = theme.Color(theme.ColorNamePrimary)
		} else {
			c.text.Color = theme.Color(theme.ColorNameDisabled)
			c.bg.FillColor = color.Transparent
		}
		c.text.Refresh()
		c.bg.Refresh()
	}
}

// Tapped toggles the cell's tag.
func (c *quickTagCell) Tapped(_ *fyne.PointEvent) {
	c.bar.toggle(c.entry.Tag)
}

// MouseIn/MouseMoved/MouseOut implement desktop.Hoverable: the status line
// names the hovered tag (icons alone can be ambiguous).
func (c *quickTagCell) MouseIn(_ *desktop.MouseEvent)    { c.bar.setStatus(c.entry.Tag) }
func (c *quickTagCell) MouseMoved(_ *desktop.MouseEvent) {}
func (c *quickTagCell) MouseOut()                        { c.bar.setStatus("") }

// grayscaleResource returns a desaturated copy of a PNG icon resource
// (alpha preserved), used as the "not applied" look for tags configured with
// only an On icon. It returns nil when the image cannot be decoded, in which
// case the cell falls back to dimming the On icon.
func grayscaleResource(res fyne.Resource) fyne.Resource {
	src, _, err := image.Decode(bytes.NewReader(res.Content()))
	if err != nil {
		return nil
	}
	bounds := src.Bounds()
	rgba := image.NewNRGBA(bounds)
	draw.Draw(rgba, bounds, src, bounds.Min, draw.Src)
	pix := rgba.Pix
	for i := 0; i+3 < len(pix); i += 4 {
		// Rec. 601 luma; alpha untouched.
		y := (299*uint32(pix[i]) + 587*uint32(pix[i+1]) + 114*uint32(pix[i+2])) / 1000
		pix[i], pix[i+1], pix[i+2] = uint8(y), uint8(y), uint8(y)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, rgba); err != nil {
		return nil
	}
	return fyne.NewStaticResource(res.Name()+"#gray", buf.Bytes())
}

// tapSink is a widget that swallows taps on the pill's padding so a slightly
// missed icon does not fall through to the ImageView (which would open the
// tag panel on desktop). It is not Draggable, so swipes still reach the
// image.
type tapSink struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func newTapSink(content fyne.CanvasObject) *tapSink {
	s := &tapSink{content: content}
	s.ExtendBaseWidget(s)
	return s
}

func (s *tapSink) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(s.content)
}

func (s *tapSink) Tapped(_ *fyne.PointEvent) {}
