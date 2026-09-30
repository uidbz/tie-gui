package ui

import (
	"image"
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// navTab identifies a compact nav bar destination.
type navTab int

const (
	tabNone navTab = iota - 1 // no tab highlighted (Now Playing)
	tabAlbums
	tabTags
	tabPlaylist
	tabSettings
	tabMenu // the ☰ slot: not a destination, never highlighted
	navTabCount
)

// navBar is the compact layout's persistent bottom navigation, pinned under
// the mini bar on every view (and alone under Now Playing): Albums, Tags,
// Playlist, Settings and the library's ☰ menu. The active destination is
// drawn in the primary color with an indicator line above it, like a
// platform bottom-navigation bar, so the user always knows where they are.
//
// It is a transportView so the Playlist tab's badge tracks the queue length
// from the player's poll; the bar itself holds no playback state.
type navBar struct {
	object  fyne.CanvasObject
	buttons [navTabCount]*navButton
	active  navTab
}

// navActions are the bar's callbacks, one per tab. menu receives the button,
// so the popup can be anchored to it.
type navActions struct {
	albums, tags, playlist, settings func()
	menu                             func(anchor fyne.CanvasObject)
}

func newNavBar(act navActions) *navBar {
	n := &navBar{active: tabAlbums}
	n.buttons[tabAlbums] = newNavButton("Albums", theme.GridIcon(), act.albums)
	n.buttons[tabTags] = newNavButton("Tags", theme.SearchIcon(), act.tags)
	n.buttons[tabPlaylist] = newNavButton("Playlist", theme.ListIcon(), act.playlist)
	n.buttons[tabSettings] = newNavButton("Settings", theme.SettingsIcon(), act.settings)
	menu := newNavButton("Menu", theme.MenuIcon(), nil)
	menu.onTap = func() {
		if act.menu != nil {
			act.menu(menu)
		}
	}
	n.buttons[tabMenu] = menu

	objs := make([]fyne.CanvasObject, 0, len(n.buttons))
	for _, b := range n.buttons {
		objs = append(objs, b)
	}
	n.object = container.NewGridWithColumns(len(objs), objs...)
	n.setActive(tabAlbums)
	return n
}

func (n *navBar) Object() fyne.CanvasObject { return n.object }

// setActive highlights tab (tabNone clears the highlight).
func (n *navBar) setActive(tab navTab) {
	n.active = tab
	for i, b := range n.buttons {
		b.setActive(navTab(i) == tab && navTab(i) != tabMenu)
	}
}

// setMenuEnabled greys out the ☰ slot on views it has nothing for (its items
// act on the album library). The slot keeps its space, so the tabs never
// shift under the thumb.
func (n *navBar) setMenuEnabled(on bool) {
	n.buttons[tabMenu].setEnabled(on)
}

// apply implements transportView: the Playlist badge shows the queue length.
func (n *navBar) apply(st transportState) {
	n.buttons[tabPlaylist].setBadge(st.queueLen)
}

func (n *navBar) setCover(image.Image) {}

// navButtonIcon is the edge of a nav button's icon.
const navButtonIcon = 24

// navButton is one nav bar destination: an icon over a caption, with an
// indicator line along the top and a count badge on the icon.
type navButton struct {
	widget.BaseWidget
	label    string
	icon     fyne.Resource
	onTap    func()
	active   bool
	disabled bool
	badge    int
}

func newNavButton(label string, icon fyne.Resource, onTap func()) *navButton {
	b := &navButton{label: label, icon: icon, onTap: onTap}
	b.ExtendBaseWidget(b)
	return b
}

func (b *navButton) setActive(on bool) {
	if b.active == on {
		return
	}
	b.active = on
	b.Refresh()
}

func (b *navButton) setEnabled(on bool) {
	if b.disabled == !on {
		return
	}
	b.disabled = !on
	b.Refresh()
}

func (b *navButton) setBadge(n int) {
	if b.badge == n {
		return
	}
	b.badge = n
	b.Refresh()
}

// Tapped implements fyne.Tappable.
func (b *navButton) Tapped(*fyne.PointEvent) {
	if b.disabled || b.onTap == nil {
		return
	}
	b.onTap()
}

func (b *navButton) CreateRenderer() fyne.WidgetRenderer {
	r := &navButtonRenderer{
		b:         b,
		indicator: canvas.NewRectangle(color.Transparent),
		icon:      canvas.NewImageFromResource(nil),
		text:      canvas.NewText(b.label, color.Black),
		badgeBg:   canvas.NewRectangle(color.Transparent),
		badgeText: canvas.NewText("", color.White),
	}
	r.icon.FillMode = canvas.ImageFillContain
	r.text.Alignment = fyne.TextAlignCenter
	r.badgeText.Alignment = fyne.TextAlignCenter
	r.badgeText.TextStyle.Bold = true
	r.Refresh()
	return r
}

type navButtonRenderer struct {
	b         *navButton
	indicator *canvas.Rectangle
	icon      *canvas.Image
	text      *canvas.Text
	badgeBg   *canvas.Rectangle
	badgeText *canvas.Text
}

const (
	navIndicatorHeight = 3
	navBadgeHeight     = 15
)

func (r *navButtonRenderer) MinSize() fyne.Size {
	pad := theme.Padding()
	textH := fyne.MeasureText(r.b.label, theme.CaptionTextSize(), fyne.TextStyle{}).Height
	w := fyne.Max(navButtonIcon, fyne.MeasureText(r.b.label, theme.CaptionTextSize(), fyne.TextStyle{}).Width)
	return fyne.NewSize(w+pad, navIndicatorHeight+pad+navButtonIcon+textH+pad)
}

func (r *navButtonRenderer) Layout(size fyne.Size) {
	pad := theme.Padding()
	r.indicator.Resize(fyne.NewSize(size.Width*0.6, navIndicatorHeight))
	r.indicator.Move(fyne.NewPos(size.Width*0.2, 0))

	iconY := navIndicatorHeight + pad
	iconX := (size.Width - navButtonIcon) / 2
	r.icon.Resize(fyne.NewSize(navButtonIcon, navButtonIcon))
	r.icon.Move(fyne.NewPos(iconX, iconY))

	textH := r.text.MinSize().Height
	r.text.Resize(fyne.NewSize(size.Width, textH))
	r.text.Move(fyne.NewPos(0, iconY+navButtonIcon))

	// The badge straddles the icon's top-right corner.
	bw := fyne.Max(navBadgeHeight, r.badgeText.MinSize().Width+6)
	r.badgeBg.Resize(fyne.NewSize(bw, navBadgeHeight))
	r.badgeBg.Move(fyne.NewPos(iconX+navButtonIcon-bw/3, iconY-navBadgeHeight/3))
	r.badgeText.Resize(fyne.NewSize(bw, navBadgeHeight))
	r.badgeText.Move(fyne.NewPos(iconX+navButtonIcon-bw/3, iconY-navBadgeHeight/3+(navBadgeHeight-r.badgeText.MinSize().Height)/2))
}

func (r *navButtonRenderer) Refresh() {
	b := r.b
	fg := theme.Color(theme.ColorNameForeground)
	var icon fyne.Resource
	switch {
	case b.disabled:
		fg = theme.Color(theme.ColorNameDisabled)
		icon = theme.NewDisabledResource(b.icon)
	case b.active:
		fg = theme.Color(theme.ColorNamePrimary)
		icon = theme.NewPrimaryThemedResource(b.icon)
	default:
		icon = theme.NewThemedResource(b.icon)
	}
	r.icon.Resource = icon
	r.icon.Refresh()
	r.text.Color = fg
	r.text.TextSize = theme.CaptionTextSize()
	r.text.TextStyle.Bold = b.active
	r.text.Refresh()

	if b.active {
		r.indicator.FillColor = theme.Color(theme.ColorNamePrimary)
	} else {
		r.indicator.FillColor = color.Transparent
	}
	r.indicator.Refresh()

	if b.badge > 0 {
		label := strconv.Itoa(b.badge)
		if b.badge > 999 {
			label = "999+"
		}
		r.badgeText.Text = label
		r.badgeText.TextSize = theme.CaptionTextSize() * 0.85
		r.badgeText.Color = theme.Color(theme.ColorNameForegroundOnPrimary)
		r.badgeBg.FillColor = theme.Color(theme.ColorNamePrimary)
		r.badgeBg.CornerRadius = navBadgeHeight / 2
		r.badgeText.Show()
		r.badgeBg.Show()
	} else {
		r.badgeText.Hide()
		r.badgeBg.Hide()
	}
	r.badgeText.Refresh()
	r.badgeBg.Refresh()
	r.Layout(b.Size())
}

func (r *navButtonRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.indicator, r.icon, r.text, r.badgeBg, r.badgeText}
}

func (r *navButtonRenderer) Destroy() {}

// panelBackground is a theme-aware filled rectangle stacked behind the
// compact bottom panel (mini bar + nav bar), so the two read as one surface
// distinct from the content above. A bare canvas.Rectangle would keep its
// color across a theme change.
type panelBackground struct {
	widget.BaseWidget
}

func newPanelBackground() *panelBackground {
	p := &panelBackground{}
	p.ExtendBaseWidget(p)
	return p
}

func (p *panelBackground) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(theme.Color(theme.ColorNameHeaderBackground))
	return &panelBackgroundRenderer{rect: rect}
}

type panelBackgroundRenderer struct{ rect *canvas.Rectangle }

func (r *panelBackgroundRenderer) Layout(size fyne.Size) { r.rect.Resize(size) }
func (r *panelBackgroundRenderer) MinSize() fyne.Size    { return fyne.NewSize(0, 0) }
func (r *panelBackgroundRenderer) Refresh() {
	r.rect.FillColor = theme.Color(theme.ColorNameHeaderBackground)
	r.rect.Refresh()
}
func (r *panelBackgroundRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.rect}
}
func (r *panelBackgroundRenderer) Destroy() {}
