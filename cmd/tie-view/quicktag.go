package main

import (
	"fmt"
	"image/color"
	"sort"
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
// icon buttons, one per configured tag, laid over the top or bottom edge of
// the single-image view so the picture stays fully visible. Tapping a button
// (or pressing its key) toggles that tag on the displayed image and writes
// the change to tie immediately; the icon flips optimistically and reverts if
// the write fails. A one-line status above/below the pill names the hovered
// button (desktop) and confirms toggles.
//
// The applied-tag set is seeded from the tieReader's expanded query
// attributes so the bar paints correctly the instant an image opens, then
// reconciled against a background tc.Get so directory-browse images (whose
// listings carry no tags) and out-of-band edits are shown correctly too.
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
	status   *widget.Label
	statusBg *canvas.Rectangle
	// statusBox centers the status label over its backdrop; it is refreshed
	// on every text change so the backdrop re-fits the new text width.
	statusBox *fyne.Container
	root      *fyne.Container // renderer content; rebuilt by Rebuild
	// Overlay is the full-size container that anchors the bar to the image
	// edge selected by cfg.Position. Append it to viewer.Content.Objects.
	Overlay *fyne.Container

	hash    string          // content hash of the displayed image ("" = none)
	reader  *tieReader      // its reader, whose tag cache is kept in sync
	applied map[string]bool // every tag on the image, not just quick ones
	pending map[string]bool // toggles made since the reconcile fetch started
	gen     int             // bumped per SetImage; stale fetches compare it
	flashID int             // bumped per status flash; stale timers compare it
	known   map[string]bool // tags registered in ("tags","all") this session

	// OnTagsChanged, when non-nil, is called on the UI goroutine after a toggle
	// with the image's full tag list, so the image tagger panel can follow.
	OnTagsChanged func(hash string, tags []string)
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
	b.root = container.NewWithoutLayout()
	b.Overlay = container.NewWithoutLayout()
	b.ExtendBaseWidget(b)
	b.Rebuild(cfg)
	return b
}

// CreateRenderer implements fyne.Widget.
func (b *quickTagBar) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(b.root)
}

// Rebuild replaces the bar's buttons from cfg (after the settings editor
// applies a change or the active collection switches) and re-anchors Overlay
// to the configured edge. The current image and its applied-tag set are kept.
func (b *quickTagBar) Rebuild(cfg QuickTagSet) {
	b.cfg = cfg.normalized()
	b.iconSize = b.cfg.IconSize
	if b.iconSize <= 0 {
		b.iconSize = quickTagIconSize(b.mobile)
	}

	b.cells = b.cells[:0]
	cellObjs := make([]fyne.CanvasObject, 0, len(b.cfg.Tags))
	for _, e := range b.cfg.Tags {
		c := newQuickTagCell(b, e, resolveQuickTagIcon(b.baseDir, e.On), resolveQuickTagIcon(b.baseDir, e.Off))
		b.cells = append(b.cells, c)
		cellObjs = append(cellObjs, c)
	}
	if len(cellObjs) == 0 {
		hint := widget.NewLabel("No quick tags configured — see Settings → Quick tags")
		cellObjs = append(cellObjs, hint)
	}

	pillBg := canvas.NewRectangle(color.NRGBA{A: 150})
	pillBg.CornerRadius = 10
	pill := newTapSink(container.NewStack(pillBg, container.NewPadded(container.NewHBox(cellObjs...))))
	b.statusBox = container.NewCenter(container.NewStack(b.statusBg, b.status))
	statusBox := b.statusBox

	var col *fyne.Container
	if b.cfg.Position == "top" {
		col = container.NewVBox(container.NewCenter(pill), statusBox)
		b.Overlay.Layout = layout.NewBorderLayout(b, nil, nil, nil)
	} else {
		col = container.NewVBox(statusBox, container.NewCenter(pill))
		b.Overlay.Layout = layout.NewBorderLayout(nil, b, nil, nil)
	}
	b.Overlay.Objects = []fyne.CanvasObject{b}
	b.root.Layout = layout.NewStackLayout()
	b.root.Objects = []fyne.CanvasObject{col}
	b.paint()
	b.root.Refresh()
	b.Refresh()
}

// Keys returns the bar's shortcut bindings: pressing an entry's key toggles
// its tag on the displayed image. Register them on each ImageView (desktop
// TypedKey) and dispatch them from the window-level handler (mobile).
func (b *quickTagBar) Keys() map[fyne.KeyName]func() {
	keys := make(map[fyne.KeyName]func(), len(b.cfg.Tags))
	for _, e := range b.cfg.Tags {
		if e.Key == "" {
			continue
		}
		tag := e.Tag
		k := fyne.KeyName(e.Key)
		if prev, dup := keys[k]; dup {
			keys[k] = func() { prev(); b.toggle(tag) }
			continue
		}
		keys[k] = func() { b.toggle(tag) }
	}
	return keys
}

// SetImage retargets the bar to the image behind r (nil clears it). The
// buttons paint from r's cached tags at once; a background tc.Get then
// reconciles the set and refreshes r's cache.
func (b *quickTagBar) SetImage(r *tieReader) {
	b.gen++
	b.pending = map[string]bool{}
	b.setStatus("")
	if r == nil {
		b.hash, b.reader = "", nil
		b.setApplied(nil)
		b.paint()
		return
	}
	b.hash, b.reader = r.hash, r
	b.setApplied(r.tags)
	b.paint()

	gen, hash := b.gen, r.hash
	go func() {
		row, err := b.tc.Get(hash)
		if err != nil {
			fmt.Println("quicktag: error fetching tags:", err)
			return
		}
		tags := client.RowValues(row, "tag")
		fyne.Do(func() {
			if b.gen != gen {
				return // stale: another image is showing
			}
			b.setApplied(tags)
			// Keep toggles the user made while the fetch was in flight.
			for tag, on := range b.pending {
				b.setTag(tag, on)
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

// Applied reports whether tag is currently applied to the displayed image.
func (b *quickTagBar) Applied(tag string) bool { return b.applied[tag] }

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

// syncReader mirrors the applied set into the reader's tag cache so a later
// SetImage for the same entry paints without waiting for the network.
func (b *quickTagBar) syncReader() {
	if b.reader != nil && b.reader.hash == b.hash {
		b.reader.setTags(b.appliedList())
	}
}

// paint pushes the applied set into every button.
func (b *quickTagBar) paint() {
	for _, c := range b.cells {
		c.setApplied(b.applied[c.entry.Tag])
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
// only an On icon it dims that icon while the tag is not applied; with none
// it shows the tag name, highlighted while applied.
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
		// Only an Off icon: treat it as the single icon and dim it when off.
		c.on, c.off = c.off, nil
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
